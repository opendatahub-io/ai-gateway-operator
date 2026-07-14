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
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
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

	rhoaiApplicationsNS   = "redhat-ods-applications"
	rhoaiInfrastructureNS = "redhat-ai-gateway-infra"
	odhApplicationsNS     = "opendatahub"
	odhInfrastructureNS   = "odh-ai-gateway-infra"
)

// deriveInfrastructureNamespace maps the applications namespace to the infrastructure
// namespace used for maas-api, postgres, and cross-namespace secret migration.
// Mirrors the logic in models-as-a-service maas-controller/cmd/manager/main.go:deriveInfraNamespace.
func deriveInfrastructureNamespace(appNs string) string {
	switch appNs {
	case rhoaiApplicationsNS:
		return rhoaiInfrastructureNS
	case odhApplicationsNS:
		return odhInfrastructureNS
	default:
		return appNs
	}
}

var batchGatewayImageParamMap = map[string]string{
	"LLM_D_BATCH_GATEWAY_OPERATOR_IMAGE":  "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_OPERATOR_IMAGE",
	"LLM_D_BATCH_GATEWAY_APISERVER_IMAGE": "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_APISERVER_IMAGE",
	"LLM_D_BATCH_GATEWAY_PROCESSOR_IMAGE": "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_PROCESSOR_IMAGE",
	"LLM_D_BATCH_GATEWAY_GC_IMAGE":        "RELATED_IMAGE_ODH_LLM_D_BATCH_GATEWAY_GC_IMAGE",
	"LLM_D_ASYNC_IMAGE":                   "RELATED_IMAGE_ODH_LLM_D_ASYNC_IMAGE",
}

var maasImageParamMap = map[string]string{
	"maas-controller-image":      "RELATED_IMAGE_ODH_MAAS_CONTROLLER_IMAGE",
	"maas-api-image":             "RELATED_IMAGE_ODH_MAAS_API_IMAGE",
	"payload-processing-image":   "RELATED_IMAGE_ODH_AI_GATEWAY_PAYLOAD_PROCESSING_IMAGE",
	"maas-api-key-cleanup-image": "RELATED_IMAGE_UBI_MINIMAL_IMAGE",
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
func (m *Module) initialize(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
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

	if obj.Spec.ModelsAsAService.ManagementState == managedState {
		rr.Manifests = append(rr.Manifests, m.maasManifestInfo)

		if rr.Client == nil {
			return fmt.Errorf("reconciliation client is nil")
		}

		monitoringNamespace, err := cluster.MonitoringNamespace(ctx, rr.Client)
		if err != nil {
			// DSCI (DSCInitialization) is OpenShift-specific and may not exist in Kind clusters
			// or standalone deployments. When DSCI is unavailable, we use the default value
			// already present in params.env. This ensures MaaS deployment succeeds on all
			// platform types.
			monitoringNamespace = ""
		}

		infraNs := deriveInfrastructureNamespace(m.cfg.ApplicationsNamespace)
		params := map[string]string{
			"namespace":                m.cfg.ApplicationsNamespace,
			"infrastructure-namespace": infraNs,
		}
		if monitoringNamespace != "" {
			params["monitoring-namespace"] = monitoringNamespace
		}

		if err := odhdeploy.ApplyParams(
			m.maasManifestInfo.String(),
			"params.env",
			nil,
			params,
		); err != nil {
			return fmt.Errorf("failed to update maas params.env: %w", err)
		}
	}

	return nil
}

// anySubModuleManaged reports whether at least one AIGateway sub-module is set to Managed.
func anySubModuleManaged(obj *componentApi.AIGateway) bool {
	return obj.Spec.BatchGateway.ManagementState == managedState ||
		obj.Spec.ModelsAsAService.ManagementState == managedState
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
