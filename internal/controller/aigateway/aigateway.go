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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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

	// Platform upgrade handshake: the platform operator maintains
	// odh-<modulename>-config with data.platformVersion. After upgrade work
	// completes, the module echoes that version into status.releases as
	// name: "platform" so the platform can detect version alignment.
	platformConfigName  = "odh-" + componentApi.AIGatewayComponentName + "-config"
	platformVersionKey  = "platformVersion"
	platformReleaseName = "platform"
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

	maasSourcePath := "base"
	if cfg.PlatformType == string(cluster.XKS) {
		maasSourcePath = "overlays/xks"
	}

	maasMI := odhtypes.ManifestInfo{
		Path:       cfg.ManifestsPath,
		ContextDir: "maascontroller",
		SourcePath: maasSourcePath,
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

	keepMaaSInstalled := obj.Spec.ModelsAsAService.ManagementState == managedState
	if !keepMaaSInstalled && obj.Spec.ModelsAsAService.ManagementState == removedState {
		var err error
		keepMaaSInstalled, err = m.shouldKeepMaaSInstalled(ctx, rr)
		if err != nil {
			return fmt.Errorf("determine MaaS teardown state: %w", err)
		}
	}

	if keepMaaSInstalled {
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
func (m *Module) overWriteCondition(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	if anySubModuleManaged(obj) {
		return nil
	}

	if rr.Client != nil {
		maasRemovalPending, err := m.maasRemovalPending(ctx, rr.Client)
		if err != nil {
			return fmt.Errorf("determine MaaS removal progress: %w", err)
		}
		if maasRemovalPending {
			rr.Conditions.MarkFalse(
				status.ConditionDeploymentsAvailable,
				conditions.WithSeverity(common.ConditionSeverityInfo),
				conditions.WithReason(status.MaaSRemovalInProgressReason),
				conditions.WithMessage("MaaS removal is in progress; waiting for remaining resources to be deleted"),
			)

			return nil
		}
	}

	rr.Conditions.MarkFalse(
		status.ConditionDeploymentsAvailable,
		conditions.WithSeverity(common.ConditionSeverityInfo),
		conditions.WithReason(status.NoSubModuleManagedReason),
		conditions.WithMessage("No sub-module is Managed; nothing to deploy"),
	)

	return nil
}

// deploymentAvailable reports whether the named Deployment in ns has at least
// one ready replica. NotFound is treated as (false, nil) — normal while a
// sub-module is starting up. Any other error is propagated so the reconciler
// requeues rather than silently sticking the condition on NotReady.
func deploymentAvailable(ctx context.Context, rr *odhtypes.ReconciliationRequest, name, ns string) (bool, error) {
	if rr.Client == nil {
		return false, nil
	}
	deploy := &appsv1.Deployment{}
	if err := rr.Client.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, deploy); err != nil {
		if k8serr.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return deploy.Status.ReadyReplicas >= 1, nil
}

// reportSubModuleStatus sets per-sub-module Ready conditions on the AIGateway CR.
// Each condition is derived from its specific Deployment so that the conditions
// are independent — one sub-module failing does not affect another's condition.
func (m *Module) reportSubModuleStatus(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	ns := m.cfg.ApplicationsNamespace

	// ModelsAsAServiceReady — reflects the readyReplicas of the modelsAsAService sub-module Deployment.
	if obj.Spec.ModelsAsAService.ManagementState == managedState {
		ready, err := deploymentAvailable(ctx, rr, maasControllerDeploymentName, ns)
		if err != nil {
			return fmt.Errorf("checking %s Deployment: %w", maasControllerDeploymentName, err)
		}
		if ready {
			rr.Conditions.MarkTrue(
				status.ConditionModelsAsAServiceReady,
				conditions.WithReason(status.SubModuleReadyReason),
				conditions.WithMessage("modelsAsAService is Managed and deployments are available"),
			)
		} else {
			rr.Conditions.MarkFalse(
				status.ConditionModelsAsAServiceReady,
				conditions.WithReason(status.SubModuleNotReadyReason),
				conditions.WithMessage("modelsAsAService is Managed but deployments are not yet available"),
			)
		}
	} else {
		rr.Conditions.MarkFalse(
			status.ConditionModelsAsAServiceReady,
			conditions.WithSeverity(common.ConditionSeverityInfo),
			conditions.WithReason(status.SubModuleRemovedReason),
			conditions.WithMessage("modelsAsAService ManagementState is Removed"),
		)
	}

	// BatchGatewayReady — reflects the readyReplicas of the batchGateway sub-module Deployment.
	if obj.Spec.BatchGateway.ManagementState == managedState {
		ready, err := deploymentAvailable(ctx, rr, batchGatewayOperatorDeploymentName, ns)
		if err != nil {
			return fmt.Errorf("checking %s Deployment: %w", batchGatewayOperatorDeploymentName, err)
		}
		if ready {
			rr.Conditions.MarkTrue(
				status.ConditionBatchGatewayReady,
				conditions.WithReason(status.SubModuleReadyReason),
				conditions.WithMessage("batchGateway is Managed and deployments are available"),
			)
		} else {
			rr.Conditions.MarkFalse(
				status.ConditionBatchGatewayReady,
				conditions.WithReason(status.SubModuleNotReadyReason),
				conditions.WithMessage("batchGateway is Managed but deployments are not yet available"),
			)
		}
	} else {
		rr.Conditions.MarkFalse(
			status.ConditionBatchGatewayReady,
			conditions.WithSeverity(common.ConditionSeverityInfo),
			conditions.WithReason(status.SubModuleRemovedReason),
			conditions.WithMessage("batchGateway ManagementState is Removed"),
		)
	}

	return nil
}

// reportStatus populates the module status with version, platform,
// and source information. When the platform config ConfigMap is present,
// it also records the platform version in status.releases (upgrade handshake).
func (m *Module) reportStatus(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	obj.Status.Module = componentApi.ModuleStatus{
		Version:     m.version,
		BuildSource: version.Repo + "@" + version.Branch + "/" + version.Commit,
		Platform:    string(rr.Release.Name),
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

	// Upgrade handshake: echo platformVersion into status.releases only after
	// earlier actions (including upgradeIfNeeded) have succeeded. When the
	// ConfigMap is absent (older platforms), skip the platform entry.
	if platformVersion := m.getPlatformVersion(ctx, rr); platformVersion != "" {
		setPlatformRelease(obj, platformVersion)
	}

	return nil
}

// getPlatformVersion reads data.platformVersion from the platform-managed
// odh-aigateway-config ConfigMap. Returns "" when the ConfigMap is missing
// or unreadable so older platforms remain compatible.
func (m *Module) getPlatformVersion(ctx context.Context, rr *odhtypes.ReconciliationRequest) string {
	log := logf.FromContext(ctx)

	cm := &corev1.ConfigMap{}
	if err := rr.Client.Get(ctx, types.NamespacedName{
		Namespace: m.cfg.ApplicationsNamespace,
		Name:      platformConfigName,
	}, cm); err != nil {
		if !k8serr.IsNotFound(err) {
			log.Error(err, "Failed to read platform config ConfigMap", "name", platformConfigName)
		}
		return ""
	}

	v := cm.Data[platformVersionKey]
	if v == "" {
		log.V(1).Info("Platform config ConfigMap has no platformVersion key", "name", platformConfigName)
	}

	return v
}

func getPlatformRelease(instance *componentApi.AIGateway) common.ComponentRelease {
	for _, r := range instance.Status.Releases {
		if r.Name == platformReleaseName {
			return r
		}
	}
	return common.ComponentRelease{}
}

func setPlatformRelease(instance *componentApi.AIGateway, platformVersion string) {
	for i, r := range instance.Status.Releases {
		if r.Name == platformReleaseName {
			instance.Status.Releases[i].Version = platformVersion
			return
		}
	}
	instance.Status.Releases = append(instance.Status.Releases, common.ComponentRelease{
		Name:    platformReleaseName,
		Version: platformVersion,
	})
}

// withPreservedPlatformRelease wraps the metadata releases action so a failed
// reconcile cannot wipe the existing platform handshake entry. The reconciler
// always patches status even when an action fails; releases.NewAction replaces
// the entire releases list, so we re-attach the previous platform version until
// reportStatus advances it after a successful reconcile.
func withPreservedPlatformRelease(
	inner func(context.Context, *odhtypes.ReconciliationRequest) error,
) func(context.Context, *odhtypes.ReconciliationRequest) error {
	return func(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
		obj, ok := rr.Instance.(*componentApi.AIGateway)
		if !ok {
			return fmt.Errorf("instance is not an AIGateway")
		}

		prev := getPlatformRelease(obj)

		err := inner(ctx, rr)

		// Always re-attach the previous platform version — whether inner
		// succeeded or failed. releases.NewAction replaces the full releases
		// list, so without this the platform handshake entry is wiped on
		// every reconcile. reportStatus advances the version only after all
		// actions complete successfully.
		if prev.Version != "" {
			setPlatformRelease(obj, prev.Version)
		}

		return err
	}
}
