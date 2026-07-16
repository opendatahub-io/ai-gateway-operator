package aigateway

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	maasGCPredicateTimeout = 10 * time.Second
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

	return m.maasControllerDeploymentExists(ctx, cli)
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

	ctx, cancel := context.WithTimeout(context.Background(), maasGCPredicateTimeout)
	defer cancel()

	return m.maasTeardownCompleted(ctx, rr.Client)
}
