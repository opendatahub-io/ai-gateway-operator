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
	"sort"

	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/conditions"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	odhdeploy "github.com/opendatahub-io/opendatahub-operator/v2/pkg/deploy"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/controller/status"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
)

// managedState is the ManagementState value that requests a sub-module be deployed.
const managedState = "Managed"

const (
	componentName = componentApi.AIGatewayComponentName
)

var batchGatewayImageParamMap = map[string]string{
	"LLM_D_BATCH_GATEWAY_OPERATOR_IMAGE":  "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_OPERATOR_IMAGE",
	"LLM_D_BATCH_GATEWAY_APISERVER_IMAGE": "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_APISERVER_IMAGE",
	"LLM_D_BATCH_GATEWAY_PROCESSOR_IMAGE": "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_PROCESSOR_IMAGE",
	"LLM_D_BATCH_GATEWAY_GC_IMAGE":        "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_GC_IMAGE",
}

var maasImageParamMap = map[string]string{
	"MAAS_CONTROLLER_IMAGE":      "RELATED_IMAGE_ODH_MAAS_CONTROLLER_IMAGE",
	"MAAS_API_IMAGE":             "RELATED_IMAGE_ODH_MAAS_API_IMAGE",
	"PAYLOAD_PROCESSING_IMAGE":   "RELATED_IMAGE_ODH_AI_GATEWAY_PAYLOAD_PROCESSING_IMAGE",
	"MAAS_API_KEY_CLEANUP_IMAGE": "RELATED_IMAGE_UBI_MINIMAL_IMAGE",
}

// Module holds process-lifetime state for the aigateway controller.
type Module struct {
	cfg                      *moduleconfig.Config
	version                  componentApi.SemVer
	batchGatewayManifestInfo odhtypes.ManifestInfo
	maasManifestInfo         odhtypes.ManifestInfo
}

// NewModule creates a Module with one-shot computed state.
func NewModule(cfg *moduleconfig.Config) (*Module, error) {
	v, err := componentApi.NewSemVer(version.Version)
	if err != nil {
		return nil, fmt.Errorf("parsing module version %q: %w", version.Version, err)
	}

	batchMI := odhtypes.ManifestInfo{
		Path:       cfg.ManifestsPath,
		ContextDir: "batchgateway",
		SourcePath: "base",
	}

	if err := odhdeploy.ApplyParams(batchMI.String(), "params.env", batchGatewayImageParamMap, nil); err != nil {
		return nil, fmt.Errorf("failed to update images on path %s: %w", batchMI, err)
	}

	maasMI := odhtypes.ManifestInfo{
		Path:       cfg.ManifestsPath,
		ContextDir: "maascontroller",
		SourcePath: "base",
	}

	if err := odhdeploy.ApplyParams(maasMI.String(), "params.env", maasImageParamMap, nil); err != nil {
		return nil, fmt.Errorf("failed to update images on path %s: %w", maasMI, err)
	}

	return &Module{
		cfg:                      cfg,
		version:                  v,
		batchGatewayManifestInfo: batchMI,
		maasManifestInfo:         maasMI,
	}, nil
}

// initialize conditionally includes batch-gateway manifests based on CRD spec.
func (m *Module) initialize(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	if obj.Spec.BatchGateway.ManagementState == managedState {
		rr.Manifests = append(rr.Manifests, m.batchGatewayManifestInfo)

		if err := odhdeploy.ApplyParams(
			m.batchGatewayManifestInfo.String(),
			"params.env",
			nil,
			map[string]string{"namespace": m.cfg.ApplicationsNamespace},
		); err != nil {
			return fmt.Errorf("failed to update batch-gateway params.env: %w", err)
		}
	}

	if obj.Spec.ModelsAsService.ManagementState == managedState {
		rr.Manifests = append(rr.Manifests, m.maasManifestInfo)

		if err := odhdeploy.ApplyParams(
			m.maasManifestInfo.String(),
			"params.env",
			nil,
			map[string]string{"namespace": m.cfg.ApplicationsNamespace},
		); err != nil {
			return fmt.Errorf("failed to update maas params.env: %w", err)
		}
	}

	return nil
}

// anySubModuleManaged reports whether at least one AIGateway sub-module is set to Managed.
func anySubModuleManaged(obj *componentApi.AIGateway) bool {
	return obj.Spec.BatchGateway.ManagementState == managedState ||
		obj.Spec.ModelsAsService.ManagementState == managedState
}

// force to set the DeploymentsAvailable condition to Info level from Error
// this makes operator not flag AIGateway CR status to False, thus opendatahub-operator wont set ModuleStatus to False
func (m *Module) overWriteCondition(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	if anySubModuleManaged(obj) {
		return nil
	}

	rr.Conditions.MarkFalse(
		status.ConditionDeploymentsAvailable,
		conditions.WithSeverity(common.ConditionSeverityInfo),
		conditions.WithReason(status.NoSubModuleManagedReason),
		conditions.WithMessage("No sub-module is Managed; nothing to deploy"),
	)

	return nil
}

// reportStatus populates the module status with version, platform,
// and source information.
func (m *Module) reportStatus(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	obj.Status.Module = componentApi.ModuleStatus{
		Version:     m.version,
		BuildSource: version.Repo + "@" + version.Branch + "/" + version.Commit,
		Platform: componentApi.PlatformStatus{
			Name:    string(rr.Release.Name),
			Version: componentApi.SemVer(rr.Release.Version.String()),
		},
	}

	var sources []componentApi.SourceStatus

	for _, manifest := range rr.Manifests {
		sources = append(sources, componentApi.SourceStatus{
			Path:     manifest.String(),
			Renderer: componentApi.SourceRendererKustomize,
		})
	}

	for _, t := range rr.Templates {
		sources = append(sources, componentApi.SourceStatus{
			Path:     t.Path,
			Renderer: componentApi.SourceRendererTemplate,
		})
	}

	for _, h := range rr.HelmCharts {
		sources = append(sources, componentApi.SourceStatus{
			Path:     h.Chart,
			Renderer: componentApi.SourceRendererHelm,
		})
	}

	sort.Slice(sources, func(i int, j int) bool {
		if sources[i].Path == sources[j].Path {
			return sources[i].Renderer < sources[j].Renderer
		}

		return sources[i].Path < sources[j].Path
	})

	obj.Status.Module.Sources = sources

	return nil
}
