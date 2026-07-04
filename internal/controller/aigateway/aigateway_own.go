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
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const maasClusterConfigName = "default"

func isCRDOrRESTMappingMiss(err error) bool {
	if k8serr.IsNotFound(err) || apimeta.IsNoMatchError(err) {
		return true
	}
	var nr *apimeta.NoResourceMatchError
	return errors.As(err, &nr)
}

// ownDerivedResources attaches AIGateway ownership to live resources that are
// created indirectly by deployed sub-components rather than by this reconcile's
// manifest bundle. This keeps the hook extensible as more derived-resource
// cases show up.
func (m *Module) ownDerivedResources(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	for _, own := range []func(context.Context, *odhtypes.ReconciliationRequest) error{
		m.ownMaaSClusterConfig,
	} {
		if err := own(ctx, rr); err != nil {
			return err
		}
	}
	return nil
}

// ownMaaSClusterConfig sets the AIGateway CR as the controller owner on the
// cluster-scoped MaaS Config/default anchor once maas-controller has created it.
func (m *Module) ownMaaSClusterConfig(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("resource instance is not AIGateway: %T", rr.Instance)
	}
	if obj.Spec.ModelsAsService.ManagementState != managedState {
		return nil
	}
	if rr.Client == nil {
		return fmt.Errorf("reconciliation client is nil")
	}

	cfg := &unstructured.Unstructured{}
	cfg.SetGroupVersionKind(gvk.MaasConfig)
	key := client.ObjectKey{Name: maasClusterConfigName}
	if err := rr.Client.Get(ctx, key, cfg); err != nil {
		if isCRDOrRESTMappingMiss(err) || k8serr.IsNotFound(err) {
			logf.FromContext(ctx).V(2).Info(
				"skipping maas Config controller reference: Config not available yet or API missing",
				"name", maasClusterConfigName,
			)
			return nil
		}
		return fmt.Errorf("get maas cluster Config %q: %w", maasClusterConfigName, err)
	}

	desired := cfg.DeepCopy()
	if err := ctrlutil.SetControllerReference(obj, desired, rr.Client.Scheme()); err != nil {
		return fmt.Errorf("maas Config %s controller reference: %w", maasClusterConfigName, err)
	}

	if equality.Semantic.DeepEqual(cfg.GetOwnerReferences(), desired.GetOwnerReferences()) {
		return nil
	}

	if err := rr.Client.Patch(ctx, desired, client.MergeFrom(cfg)); err != nil {
		return fmt.Errorf("patch maas Config %s ownerReferences: %w", maasClusterConfigName, err)
	}

	return nil
}
