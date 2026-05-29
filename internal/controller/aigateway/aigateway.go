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

	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	odhdeploy "github.com/opendatahub-io/opendatahub-operator/v2/pkg/deploy"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/version"
)

const (
	componentName = componentApi.AIGatewayComponentName
)

var batchGatewayImageParamMap = map[string]string{
	"BATCH_GATEWAY_OPERATOR_IMAGE": "RELATED_IMAGE_ODH_BATCH_GATEWAY_OPERATOR_IMAGE",
}

// Module holds process-lifetime state for the aigateway controller.
type Module struct {
	cfg                      *moduleconfig.Config
	version                  componentApi.SemVer
	batchGatewayManifestInfo odhtypes.ManifestInfo
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

	return &Module{
		cfg:                      cfg,
		version:                  v,
		batchGatewayManifestInfo: batchMI,
	}, nil
}

// initialize conditionally includes batch-gateway manifests based on CRD spec.
func (m *Module) initialize(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	if obj.Spec.BatchGateway.ManagementState == "Managed" {
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
