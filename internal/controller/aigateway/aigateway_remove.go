package aigateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/gc"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const (
	removedState                           = "Removed"
	maasControllerDeploymentName           = "maas-controller"
	aiGatewayControllerDeploymentName      = "ai-gateway-controller"
	batchGatewayOperatorDeploymentName     = "llm-d-batch-gateway-operator"
	aiGridOperatorDeploymentName           = "grid-operator"
	maasTeardownRequestedKey               = "maas.opendatahub.io/teardown-requested"
	maasTeardownCompletedKey               = "maas.opendatahub.io/teardown-completed"
	maasTeardownRequestedAtKey             = "maas.opendatahub.io/teardown-requested-at"
	maasTeardownRequeuedAtKey              = "maas.opendatahub.io/teardown-requeued-at"
	maasCRDComponentLabelKey               = "app.kubernetes.io/component"
	maasCRDComponentLabelValue             = "models-as-a-service"
	aiGatewayControllerComponentLabelValue = "ai-gateway-controller"

	maasGCPredicateTimeout = 10 * time.Second
	maasTeardownTimeout    = 2 * time.Minute
	maasTeardownRetryDelay = 5 * time.Second
)

type maasTeardownTimer interface {
	Stop() bool
}

// shouldKeepMaaSInstalled reports whether the vendored maas-controller bundle
// should remain rendered while teardown is in progress. AI Gateway keeps
// maas-controller installed until it reports completion on its Deployment, or
// until the bounded teardown deadline expires.
func (m *Module) shouldKeepMaaSInstalled(ctx context.Context, rr *odhtypes.ReconciliationRequest) (bool, error) {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return false, fmt.Errorf("instance is not an AIGateway")
	}
	if obj.Spec.ModelsAsAService.ManagementState != removedState {
		return false, nil
	}
	if rr.Client == nil {
		return false, nil
	}

	deadlineReached, err := m.ensureMaasTeardownDeadline(ctx, rr)
	if err != nil {
		return false, err
	}
	if deadlineReached {
		m.stopMaasTeardownRequeue()
		if err := deleteMaasConfig(ctx, rr.Client); err != nil {
			return false, err
		}
		return false, nil
	}

	completed, err := m.maasTeardownCompleted(ctx, rr.Client)
	if err != nil {
		return false, err
	}
	if completed {
		m.stopMaasTeardownRequeue()
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

func (m *Module) maasRemovalPending(ctx context.Context, rr *odhtypes.ReconciliationRequest) (bool, error) {
	if rr.Client == nil {
		return false, nil
	}

	deadlineReached := m.maasTeardownDeadlineReached(rr.Instance)
	finished, err := m.maasTeardownFinished(ctx, rr.Client, deadlineReached)
	if err != nil {
		return false, err
	}
	if !finished {
		return true, nil
	}

	return m.maasControllerDeploymentExists(ctx, rr.Client)
}

func (m *Module) maasTeardownStalled(ctx context.Context, cli client.Client) (bool, error) {
	if cli == nil {
		return false, nil
	}

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
		return deployment.GetAnnotations()[maasTeardownRequestedKey] == "true" &&
			deployment.GetAnnotations()[maasTeardownCompletedKey] != "true", nil
	}
}

func (m *Module) maasTeardownFinished(ctx context.Context, cli client.Client, deadlineReached bool) (bool, error) {
	if deadlineReached {
		return true, nil
	}

	completed, err := m.maasTeardownCompleted(ctx, cli)
	if err != nil {
		return false, err
	}
	return completed, nil
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

func (m *Module) ensureMaasTeardownDeadline(ctx context.Context, rr *odhtypes.ReconciliationRequest) (bool, error) {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return false, fmt.Errorf("instance is not an AIGateway")
	}

	annotations := obj.GetAnnotations()
	if value, ok := annotations[maasTeardownRequestedAtKey]; ok {
		requestedAt, err := time.Parse(time.RFC3339, value)
		if err == nil {
			deadline := requestedAt.Add(maasTeardownTimeout)
			deadlineReached := !deadline.After(m.maasTeardownNow())
			if !deadlineReached {
				m.scheduleMaasTeardownRequeue(rr.Client, client.ObjectKeyFromObject(obj), deadline.Sub(m.maasTeardownNow()))
			}
			return deadlineReached, nil
		}
	}

	before := obj.DeepCopy()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[maasTeardownRequestedAtKey] = m.maasTeardownNow().UTC().Format(time.RFC3339)
	obj.SetAnnotations(annotations)

	if rr.Client == nil {
		return false, nil
	}
	if err := rr.Client.Patch(ctx, obj, client.MergeFrom(before)); err != nil {
		return false, fmt.Errorf("annotate AIGateway with MaaS teardown deadline: %w", err)
	}
	m.scheduleMaasTeardownRequeue(rr.Client, client.ObjectKeyFromObject(obj), maasTeardownTimeout)

	return false, nil
}

func (m *Module) clearMaasTeardownDeadline(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}
	if _, exists := obj.GetAnnotations()[maasTeardownRequestedAtKey]; !exists {
		return nil
	}
	m.stopMaasTeardownRequeue()
	if rr.Client == nil {
		return nil
	}

	before := obj.DeepCopy()
	annotations := obj.GetAnnotations()
	delete(annotations, maasTeardownRequestedAtKey)
	delete(annotations, maasTeardownRequeuedAtKey)
	obj.SetAnnotations(annotations)

	if err := rr.Client.Patch(ctx, obj, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("remove MaaS teardown deadline annotation: %w", err)
	}

	return nil
}

func (m *Module) scheduleMaasTeardownRequeue(cli client.Client, key client.ObjectKey, delay time.Duration) {
	if delay < 0 {
		delay = 0
	}

	m.maasTeardownMu.Lock()
	defer m.maasTeardownMu.Unlock()

	if m.maasTeardownTimer != nil {
		m.maasTeardownTimer.Stop()
	}
	after := m.maasTeardownAfter
	if after == nil {
		after = func(delay time.Duration, action func()) maasTeardownTimer {
			return time.AfterFunc(delay, action)
		}
	}
	m.maasTeardownTimer = after(delay, func() {
		if err := m.requeueMaasTeardown(cli, key); err != nil {
			m.scheduleMaasTeardownRequeue(cli, key, maasTeardownRetryDelay)
		}
	})
}

func (m *Module) stopMaasTeardownRequeue() {
	m.maasTeardownMu.Lock()
	defer m.maasTeardownMu.Unlock()

	if m.maasTeardownTimer != nil {
		m.maasTeardownTimer.Stop()
		m.maasTeardownTimer = nil
	}
}

func (m *Module) requeueMaasTeardown(cli client.Client, key client.ObjectKey) error {
	ctx, cancel := context.WithTimeout(context.Background(), maasGCPredicateTimeout)
	defer cancel()

	obj := &componentApi.AIGateway{}
	if err := cli.Get(ctx, key, obj); err != nil {
		if k8serr.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get AIGateway %s for MaaS teardown requeue: %w", key.Name, err)
	}
	if obj.Spec.ModelsAsAService.ManagementState != removedState || !m.maasTeardownDeadlineReached(obj) {
		return nil
	}

	before := obj.DeepCopy()
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[maasTeardownRequeuedAtKey] = time.Now().UTC().Format(time.RFC3339Nano)
	obj.SetAnnotations(annotations)

	if err := cli.Patch(ctx, obj, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("requeue AIGateway %s for MaaS teardown: %w", key.Name, err)
	}

	return nil
}

func (m *Module) maasTeardownDeadlineReached(instance client.Object) bool {
	value := instance.GetAnnotations()[maasTeardownRequestedAtKey]
	if value == "" {
		return false
	}

	requestedAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return false
	}

	return !requestedAt.Add(maasTeardownTimeout).After(m.maasTeardownNow())
}

func deleteMaasConfig(ctx context.Context, cli client.Client) error {
	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(maasConfigGVK)
	cfg.SetName(maasConfigName)

	if err := cli.Delete(ctx, cfg); err != nil {
		if k8serr.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("delete MaaS Config %s: %w", maasConfigName, err)
	}

	return nil
}

// maasAwareGCPredicate augments gc.DefaultObjectPredicate for the gc.NewAction
// pipeline step: besides the default generation-based staleness check, maas-controller
// bundle resources also become deletable once maas-controller has reported
// completion or the bounded teardown deadline has expired.
// The AIGateway CR's .metadata.generation never changes as part of that signal - nothing
// about the AIGateway spec changed, completion is signaled out-of-band via the
// maas-controller Deployment's own annotation - so the default predicate alone would
// never consider either bundle's resources eligible for collection.
func (m *Module) maasAwareGCPredicate(rr *odhtypes.ReconciliationRequest, obj unstructured.Unstructured) (bool, error) {
	// Never GC grid CRDs: deleting one cascades to every grid custom resource.
	if obj.GroupVersionKind() == gvk.CustomResourceDefinition && strings.HasSuffix(obj.GetName(), aiGridCRDSuffix) {
		return false, nil
	}

	deletable, err := gc.DefaultObjectPredicate(rr, obj)
	if err != nil || deletable {
		return deletable, err
	}

	component := obj.GetLabels()[maasCRDComponentLabelKey]
	if component != maasCRDComponentLabelValue && component != aiGatewayControllerComponentLabelValue {
		return false, nil
	}
	if rr.Client == nil {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), maasGCPredicateTimeout)
	defer cancel()

	return m.maasTeardownFinished(ctx, rr.Client, m.maasTeardownDeadlineReached(rr.Instance))
}
