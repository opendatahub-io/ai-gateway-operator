/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aigateway

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

// migrateSelector is the action pipeline entry point (see NewReconciler) for
// deleting Deployments whose spec.selector.matchLabels is stale relative to
// what the current AIGateway module would render. It must run after
// kustomize.NewAction (so the expected labels are known) and before
// deploy.NewAction (so a stale Deployment is gone before deploy tries to
// apply the new selector).
//
// Currently this only covers maas-controller; see migrateMaasControllerSelector.
func (m *Module) migrateSelector(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	return m.migrateMaasControllerSelector(ctx, rr)
}

// maasControllerRequiredSelectorLabels are the labels the kustomize.NewAction
// pipeline step (see NewReconciler) stamps onto every rendered Deployment's
// spec.selector.matchLabels, including maas-controller's. A live Deployment
// must carry these in its selector to be considered current.
var maasControllerRequiredSelectorLabels = map[string]string{
	labels.K8SCommon.PartOf:             componentName,
	labels.ODH.Component(componentName): labels.True,
}

// maasControllerLegacySelectorLabelKeys are selector label keys that only ever
// appeared on maas-controller's Deployment before it was owned by the AIGateway
// module, when it was deployed by the standalone modelsasservice component.
//
// Unlike app.kubernetes.io/part-of, whose legacy and current values share a
// single key (so the old and new values can never coexist), the legacy
// app.opendatahub.io/modelsasservice key is distinct from the current
// app.opendatahub.io/aigateway key: a Deployment recreated or patched with a
// merged selector could carry both alongside maasControllerRequiredSelectorLabels.
// selectorHasRequiredLabels must reject the presence of any of these keys
// outright — otherwise this migration would treat such a selector as current,
// and deploy.NewAction would still fail with "field is immutable" trying to
// apply a selector that drops them.
var maasControllerLegacySelectorLabelKeys = []string{
	labels.ODH.Component("modelsasservice"),
}

// migrateMaasControllerSelector deletes the maas-controller Deployment when its
// spec.selector.matchLabels is missing the labels the AIGateway module stamps on
// it (see maasControllerRequiredSelectorLabels).
//
// Before this module existed, maas-controller was deployed by the standalone
// modelsasservice component with selector labels
// app.kubernetes.io/part-of=modelsasservice and app.opendatahub.io/modelsasservice=true.
// AIGateway now stamps app.kubernetes.io/part-of=aigateway and
// app.opendatahub.io/aigateway=true instead. Since spec.selector is immutable on
// Deployments, upgrading from the old component fails on every reconcile with:
//
//	spec.selector: Invalid value: ...: field is immutable
//
// The only way to move the selector forward is to delete the stale Deployment
// and let deploy.NewAction recreate it with the current selector.
//
// The check only requires maasControllerRequiredSelectorLabels to be a subset of
// the live selector (not exact equality), so this is a no-op once the Deployment
// carries the current labels even though the selector also has other entries
// (e.g. control-plane) that are not part of the migration. It additionally
// rejects any maasControllerLegacySelectorLabelKeys outright, since those can
// coexist with the current labels without a key collision (see its doc comment).
func (m *Module) migrateMaasControllerSelector(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	if rr.Client == nil {
		return fmt.Errorf("reconciliation client is nil")
	}

	dep := &appsv1.Deployment{}
	err := rr.Client.Get(ctx, client.ObjectKey{
		Name:      maasControllerDeploymentName,
		Namespace: m.cfg.ApplicationsNamespace,
	}, dep)
	if err != nil {
		if k8serr.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get maas-controller Deployment %s/%s: %w", m.cfg.ApplicationsNamespace, maasControllerDeploymentName, err)
	}

	if selectorHasRequiredLabels(dep, maasControllerRequiredSelectorLabels, maasControllerLegacySelectorLabelKeys) {
		return nil
	}

	var currentSelector map[string]string
	if dep.Spec.Selector != nil {
		currentSelector = dep.Spec.Selector.MatchLabels
	}

	logf.FromContext(ctx).Info("maas-controller Deployment has a stale selector, deleting for recreation",
		"namespace", m.cfg.ApplicationsNamespace,
		"deployment", maasControllerDeploymentName,
		"currentSelector", currentSelector,
	)

	// Guard against a Get/Delete race (CWE-367): if the Deployment was deleted
	// and recreated (e.g. by a concurrent reconcile, GitOps tool, or manual
	// kubectl action) between the Get above and this Delete, dep no longer
	// refers to the live object. Preconditions make the Delete fail with a
	// Conflict instead of removing whatever object now has this name — UID
	// pins the exact object identity we read; ResourceVersion is included
	// too since it's the only precondition the fake client honors in tests.
	preconditions := &client.Preconditions{UID: &dep.UID, ResourceVersion: &dep.ResourceVersion}
	if err := rr.Client.Delete(ctx, dep, preconditions); err != nil && !k8serr.IsNotFound(err) && !k8serr.IsConflict(err) {
		return fmt.Errorf("delete maas-controller Deployment %s/%s with stale selector: %w", m.cfg.ApplicationsNamespace, maasControllerDeploymentName, err)
	}

	return nil
}

// selectorHasRequiredLabels reports whether the Deployment's
// spec.selector.matchLabels contains every key/value in required and none of
// the keys listed in forbidden.
func selectorHasRequiredLabels(dep *appsv1.Deployment, required map[string]string, forbidden []string) bool {
	if dep.Spec.Selector == nil {
		return false
	}

	for k, v := range required {
		if dep.Spec.Selector.MatchLabels[k] != v {
			return false
		}
	}

	for _, k := range forbidden {
		if _, present := dep.Spec.Selector.MatchLabels[k]; present {
			return false
		}
	}

	return true
}
