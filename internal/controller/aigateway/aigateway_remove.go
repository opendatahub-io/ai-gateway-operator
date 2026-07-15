package aigateway

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlpredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/gc"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const (
	removedState                 = "Removed"
	maasControllerDeploymentName = "maas-controller"
	maasTeardownRequestedKey     = "maas.opendatahub.io/teardown-requested"
	maasTeardownCompletedKey     = "maas.opendatahub.io/teardown-completed"
	maasCRDComponentLabelKey     = "app.kubernetes.io/component"
	maasCRDComponentLabelValue   = "models-as-a-service"
	maasCRDNameLabelKey          = "app.kubernetes.io/name"
	maasCRDNameLabelValue        = "maas-controller"
)

// shouldKeepMaaSInstalled reports whether the vendored maas-controller bundle
// should remain rendered while teardown is in progress. AI Gateway keeps
// maas-controller installed until it reports, via TeardownCompletedAnnotation
// on its own Deployment, that it has finished its own self-teardown.
func (m *Module) shouldKeepMaaSInstalled(ctx context.Context, rr *odhtypes.ReconciliationRequest) (bool, error) {
	if rr.Client == nil {
		return false, nil
	}
	completed, err := m.maasTeardownCompleted(ctx, rr.Client)
	if err != nil {
		return false, err
	}
	return !completed, nil
}

// annotateResource is the pipeline-facing entry point for annotating rendered
// resources ahead of this pass's apply. Currently this only covers requesting
// maas-controller's self-teardown; see annotateMaaSRequestedTeardown.
func (m *Module) annotateResource(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	return m.annotateMaaSRequestedTeardown(ctx, rr)
}

// annotateMaaSRequestedTeardown annotates the rendered maas-controller Deployment so
// MaaS starts its own teardown flow (disable self-heal, delete Config/default,
// and clean up runtime resources) while AI Gateway keeps the controller
// installed long enough for cleanup to finish.
func (m *Module) annotateMaaSRequestedTeardown(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}
	if obj.Spec.ModelsAsAService.ManagementState != removedState {
		return nil
	}

	for i := range rr.Resources {
		resource := &rr.Resources[i]
		if resource.GroupVersionKind() != gvk.Deployment {
			continue
		}
		if resource.GetName() != maasControllerDeploymentName || resource.GetNamespace() != m.cfg.ApplicationsNamespace {
			continue
		}

		annotations := resource.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[maasTeardownRequestedKey] = "true"
		resource.SetAnnotations(annotations)
	}

	return nil
}

// cleanupCRD is the pipeline-facing entry point for removing component-owned
// CRDs once that component's runtime cleanup has finished. Currently this only
// covers the vendored MaaS CRDs; see cleanupMaaSCRD.
func (m *Module) cleanupCRD(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	return m.cleanupMaaSCRD(ctx, rr)
}

// cleanupMaaSCRD waits for maas-controller to report (via TeardownCompletedAnnotation
// on its own Deployment) that its self-teardown is done, then performs the final
// uninstall steps still owned by AI Gateway: waiting for the Deployment itself to be
// garbage-collected (it was excluded from this pass's render by shouldKeepMaaSInstalled
// once completion was observed) and deleting the vendored MaaS CRDs.
func (m *Module) cleanupMaaSCRD(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}
	if obj.Spec.ModelsAsAService.ManagementState != removedState {
		return nil
	}
	if rr.Client == nil {
		return fmt.Errorf("reconciliation client is nil")
	}

	completed, err := m.maasTeardownCompleted(ctx, rr.Client)
	if err != nil {
		return err
	}
	if !completed {
		return nil
	}

	controllerPresent, err := m.maasControllerDeploymentExists(ctx, rr.Client)
	if err != nil {
		return err
	}
	if controllerPresent {
		return nil
	}

	crdsPending, err := m.ensureMaaSCRDsDeleted(ctx, rr.Client)
	if err != nil {
		return err
	}
	if crdsPending {
		return nil
	}

	return nil
}

func (m *Module) maasRemovalPending(ctx context.Context, cli client.Client) (bool, error) {
	if cli == nil {
		return false, nil
	}

	completed, err := m.maasTeardownCompleted(ctx, cli)
	if err != nil {
		return false, err
	}
	if !completed {
		return true, nil
	}

	controllerPresent, err := m.maasControllerDeploymentExists(ctx, cli)
	if err != nil {
		return false, err
	}
	if controllerPresent {
		return true, nil
	}

	return m.maasCRDsRemain(ctx, cli)
}

func (m *Module) maasControllerDeploymentExists(ctx context.Context, cli client.Client) (bool, error) {
	var deployment appsv1.Deployment
	err := cli.Get(ctx, client.ObjectKey{
		Namespace: m.cfg.ApplicationsNamespace,
		Name:      maasControllerDeploymentName,
	}, &deployment)
	switch {
	case client.IgnoreNotFound(err) == nil && err != nil:
		return false, nil
	case err != nil:
		return false, fmt.Errorf("get maas-controller Deployment %s/%s: %w",
			m.cfg.ApplicationsNamespace, maasControllerDeploymentName, err)
	default:
		return true, nil
	}
}

// maasTeardownCompleted reports whether maas-controller has finished its own
// self-teardown, per TeardownCompletedAnnotation on its Deployment. A missing
// Deployment is treated as completed too: there is nothing left to wait on.
func (m *Module) maasTeardownCompleted(ctx context.Context, cli client.Client) (bool, error) {
	var deployment appsv1.Deployment
	err := cli.Get(ctx, client.ObjectKey{
		Namespace: m.cfg.ApplicationsNamespace,
		Name:      maasControllerDeploymentName,
	}, &deployment)
	switch {
	case client.IgnoreNotFound(err) == nil && err != nil:
		return true, nil
	case err != nil:
		return false, fmt.Errorf("get maas-controller Deployment %s/%s: %w",
			m.cfg.ApplicationsNamespace, maasControllerDeploymentName, err)
	default:
		return deployment.GetAnnotations()[maasTeardownCompletedKey] == "true", nil
	}
}

// maasAwareGCPredicate augments gc.DefaultObjectPredicate for the gc.NewAction
// pipeline step: besides the default generation-based staleness check, maas-controller
// bundle resources (identified by their app.kubernetes.io/component=models-as-a-service
// label) also become deletable once maas-controller has reported completion via
// TeardownCompletedAnnotation. The AIGateway CR's .metadata.generation never changes as
// part of that signal - nothing about the AIGateway spec changed, completion is signaled
// out-of-band via the maas-controller Deployment's own annotation - so the default
// predicate alone would never consider these resources eligible for collection.
func (m *Module) maasAwareGCPredicate(rr *odhtypes.ReconciliationRequest, obj unstructured.Unstructured) (bool, error) {
	deletable, err := gc.DefaultObjectPredicate(rr, obj)
	if err != nil || deletable {
		return deletable, err
	}

	if obj.GetLabels()[maasCRDComponentLabelKey] != maasCRDComponentLabelValue {
		return false, nil
	}
	if rr.Client == nil {
		return false, nil
	}

	return m.maasTeardownCompleted(context.Background(), rr.Client)
}

func (m *Module) maasCRDsRemain(ctx context.Context, cli client.Client) (bool, error) {
	crds, err := m.listMaaSCRDs(ctx, cli)
	if err != nil {
		return false, err
	}
	return len(crds.Items) > 0, nil
}

func (m *Module) ensureMaaSCRDsDeleted(ctx context.Context, cli client.Client) (bool, error) {
	crds, err := m.listMaaSCRDs(ctx, cli)
	if err != nil {
		return false, err
	}

	pending := false
	for i := range crds.Items {
		crd := &crds.Items[i]
		pending = true
		if !crd.GetDeletionTimestamp().IsZero() {
			continue
		}
		if err := cli.Delete(ctx, crd); client.IgnoreNotFound(err) != nil {
			return false, fmt.Errorf("delete MaaS CRD %q: %w", crd.GetName(), err)
		}
	}

	return pending, nil
}

func (m *Module) listMaaSCRDs(ctx context.Context, cli client.Client) (*extv1.CustomResourceDefinitionList, error) {
	list := &extv1.CustomResourceDefinitionList{}
	if err := cli.List(ctx, list, client.MatchingLabels{
		maasCRDComponentLabelKey: maasCRDComponentLabelValue,
		maasCRDNameLabelKey:      maasCRDNameLabelValue,
	}); err != nil {
		return nil, fmt.Errorf("list MaaS CRDs: %w", err)
	}
	return list, nil
}

func watchDefaultAIGateway(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: componentApi.AIGatewayInstanceName},
	}}
}

func maasCRDWatchPredicate() ctrlpredicate.Predicate {
	return ctrlpredicate.NewPredicateFuncs(func(obj client.Object) bool {
		labels := obj.GetLabels()
		return labels != nil &&
			labels[maasCRDComponentLabelKey] == maasCRDComponentLabelValue &&
			labels[maasCRDNameLabelKey] == maasCRDNameLabelValue
	})
}
