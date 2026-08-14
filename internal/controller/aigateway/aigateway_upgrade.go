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

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

func (m *Module) upgradeIfNeeded(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	prev := obj.Status.Module

	// Platform-version upgrade detection is intentionally omitted: the platform
	// version now lives only in status.releases (the ConfigMap-sourced upgrade
	// handshake). When upgrade() does real work, compare against that entry
	// rather than rr.Release, which is not reliably populated in this operator.
	moduleVersionChanged := !prev.Version.IsZero() && m.version.GT(prev.Version)

	if !moduleVersionChanged {
		return nil
	}

	return m.upgrade(ctx, prev, rr)
}

func (m *Module) upgrade(_ context.Context, prev componentApi.ModuleStatus, rr *odhtypes.ReconciliationRequest) error {
	_ = prev
	_ = rr

	return nil
}
