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
// (e.g. control-plane) that are not part of the migration.
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

	if selectorHasRequiredLabels(dep, maasControllerRequiredSelectorLabels) {
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

	if err := rr.Client.Delete(ctx, dep); err != nil && !k8serr.IsNotFound(err) {
		return fmt.Errorf("delete maas-controller Deployment %s/%s with stale selector: %w", m.cfg.ApplicationsNamespace, maasControllerDeploymentName, err)
	}

	return nil
}

// selectorHasRequiredLabels reports whether required is a subset of the
// Deployment's spec.selector.matchLabels.
func selectorHasRequiredLabels(dep *appsv1.Deployment, required map[string]string) bool {
	if dep.Spec.Selector == nil {
		return false
	}

	for k, v := range required {
		if dep.Spec.Selector.MatchLabels[k] != v {
			return false
		}
	}

	return true
}
