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
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/pkg/controller/status"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/deploy"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/gc"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/render/kustomize"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/status/deployments"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/status/releases"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/predicates"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/reconciler"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

// Module operator's own CRD
// +kubebuilder:rbac:groups=components.platform.opendatahub.io,resources=aigateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=components.platform.opendatahub.io,resources=aigateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=components.platform.opendatahub.io,resources=aigateways/finalizers,verbs=update

// Resources deployed by the batch-gateway operator kustomize manifests
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;serviceaccounts;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;get;list;patch;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=create;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,resourceNames=llm-d-batch-gateway-operator;llmbatchgateway-admin;llmbatchgateway-view,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,resourceNames=llm-d-batch-gateway-operator,verbs=get;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=create;list;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,resourceNames=llmbatchgateways.batch.llm-d.ai,verbs=get;update;patch;delete

// Batch-gateway operator RBAC escalation
// +kubebuilder:rbac:groups=batch.llm-d.ai,resources=llmbatchgateways,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups=batch.llm-d.ai,resources=llmbatchgateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch.llm-d.ai,resources=llmbatchgateways/status,verbs=get;patch;update
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=referencegrants,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=podmonitors;prometheusrules;servicemonitors,verbs=get;list;watch;create;update;patch;delete

// MaaS controller deployment - needs permissions to create/patch CRDs, ClusterRoles, and ValidatingWebhookConfigurations
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;update;patch
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;watch;create;update;patch;delete

// MaaS controller RBAC escalation - explicit resources instead of wildcard
// +kubebuilder:rbac:groups=maas.opendatahub.io,resources=tenants;aitenants;maasmodelrefs;maasauthpolicies;maassubscriptions;configs;externalmodels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=maas.opendatahub.io,resources=tenants/status;aitenants/status;maasmodelrefs/status;maasauthpolicies/status;maassubscriptions/status;configs/status;externalmodels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=maas.opendatahub.io,resources=tenants/finalizers;aitenants/finalizers;maasmodelrefs/finalizers;maasauthpolicies/finalizers;maassubscriptions/finalizers;configs/finalizers;externalmodels/finalizers,verbs=update
// +kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels;externalproviders,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels/finalizers;externalproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels/status;externalproviders/status,verbs=get;patch;update
// +kubebuilder:rbac:groups="",resources=namespaces;endpoints;pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=create;patch;update
// +kubebuilder:rbac:groups=apps,resources=deployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.openshift.io,resources=authentications,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/finalizers,verbs=update
// +kubebuilder:rbac:groups=kuadrant.io,resources=authpolicies;ratelimitpolicies;tokenratelimitpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions.kuadrant.io,resources=telemetrypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.istio.io,resources=destinationrules;envoyfilters;serviceentries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.authorino.kuadrant.io,resources=authorinos,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.kserve.io,resources=llminferenceservices,verbs=get;list;watch
// +kubebuilder:rbac:groups=telemetry.istio.io,resources=telemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=perses.dev,resources=persesdashboards;persesdatasources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=delete

func NewReconciler(
	ctx context.Context,
	mgr ctrl.Manager,
	cfg *moduleconfig.Config,
	rel common.Release,
) error {
	m, err := NewModule(cfg)
	if err != nil {
		return err
	}

	r, err := reconciler.ReconcilerFor(mgr, &componentApi.AIGateway{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&rbacv1.ClusterRoleBinding{}).
		Owns(&rbacv1.ClusterRole{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&rbacv1.Role{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&apiextensionsv1.CustomResourceDefinition{}).
		Owns(&appsv1.Deployment{}, reconciler.WithPredicates(predicates.DefaultDeploymentPredicate)).
		WithAction(m.initialize).
		WithAction(m.upgradeIfNeeded).
		WithAction(releases.NewAction(
			releases.WithMetadataFilePath(func(rr *odhtypes.ReconciliationRequest) string {
				return filepath.Join(rr.ManifestsBasePath, "ai-gateway-operator", releases.ComponentMetadataFilename)
			}),
		)).
		WithAction(kustomize.NewAction(
			kustomize.WithLabel(labels.ODH.Component(componentName), labels.True),
			kustomize.WithLabel(labels.K8SCommon.PartOf, componentName),
		)).
		WithAction(deploy.NewAction(
			deploy.WithCache(),
			deploy.WithApplyOrder(),
		)).
		WithAction(deployments.NewAction()).
		WithAction(m.overWriteCondition).
		WithAction(m.reportStatus).
		WithAction(gc.NewAction(
			gc.InNamespace(cfg.ApplicationsNamespace),
		)).
		WithConditions(
			status.ConditionDeploymentsAvailable,
		).
		Build(ctx)

	if err != nil {
		return err
	}

	r.Release = rel

	return nil
}
