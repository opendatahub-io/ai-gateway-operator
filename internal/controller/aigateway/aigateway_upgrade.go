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

	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
)

func (m *Module) upgradeIfNeeded(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	prev := obj.Status.Module

	moduleVersionChanged := !prev.Version.IsZero() && m.version.GT(prev.Version)
	platformVersionChanged := !prev.Platform.Version.IsZero() &&
		componentApi.SemVer(rr.Release.Version.String()).GT(prev.Platform.Version)

	if !moduleVersionChanged && !platformVersionChanged {
		return nil
	}

	return m.upgrade(ctx, prev, rr)
}

func (m *Module) upgrade(_ context.Context, prev componentApi.ModuleStatus, rr *odhtypes.ReconciliationRequest) error {
	_ = prev
	_ = rr

	return nil
}
