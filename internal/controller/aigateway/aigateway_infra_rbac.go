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

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const (
	secretMigrateRoleName = "maas-controller-secret-migrate"
	maasControllerSAName  = "maas-controller"
	maasDBConfigSecret    = "maas-db-config"
)

// ensureInfraSecretMigrationRBAC creates namespace-scoped RBAC in the infrastructure
// namespace so the maas-controller can copy the maas-db-config secret during upgrades.
func (m *Module) ensureInfraSecretMigrationRBAC(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return fmt.Errorf("instance is not an AIGateway")
	}

	if obj.Spec.ModelsAsAService.ManagementState != managedState {
		return nil
	}

	infraNs := deriveInfrastructureNamespace(m.cfg.ApplicationsNamespace)
	if infraNs == m.cfg.ApplicationsNamespace {
		return nil
	}

	logger := log.FromContext(ctx).WithValues(
		"infraNamespace", infraNs,
		"applicationsNamespace", m.cfg.ApplicationsNamespace,
	)

	if err := ensureNamespace(ctx, rr.Client, infraNs); err != nil {
		return fmt.Errorf("ensuring infrastructure namespace %s: %w", infraNs, err)
	}

	if err := ensureSecretMigrateRole(ctx, rr.Client, obj, infraNs); err != nil {
		return fmt.Errorf("ensuring secret-migrate Role in %s: %w", infraNs, err)
	}

	if err := ensureSecretMigrateRoleBinding(ctx, rr.Client, obj, infraNs, m.cfg.ApplicationsNamespace); err != nil {
		return fmt.Errorf("ensuring secret-migrate RoleBinding in %s: %w", infraNs, err)
	}

	logger.V(1).Info("infrastructure namespace secret migration RBAC is ready")

	return nil
}

func ensureNamespace(ctx context.Context, cli client.Client, name string) error {
	ns := &corev1.Namespace{}
	if err := cli.Get(ctx, types.NamespacedName{Name: name}, ns); err != nil {
		if !k8serr.IsNotFound(err) {
			return err
		}
		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					"app.kubernetes.io/part-of":    "ai-gateway",
					"app.kubernetes.io/managed-by": "ai-gateway-operator",
				},
			},
		}
		return cli.Create(ctx, ns)
	}
	return nil
}

func ensureSecretMigrateRole(ctx context.Context, cli client.Client, owner *componentApi.AIGateway, namespace string) error {
	desired := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretMigrateRoleName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":    "models-as-a-service",
				"app.kubernetes.io/managed-by": "ai-gateway-operator",
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{maasDBConfigSecret},
				Verbs:         []string{"get"},
			},
		},
	}

	existing := &rbacv1.Role{}
	err := cli.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if k8serr.IsNotFound(err) {
		if err := controllerutil.SetOwnerReference(owner, desired, cli.Scheme()); err != nil {
			return fmt.Errorf("setting owner reference: %w", err)
		}
		return cli.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if err := controllerutil.SetOwnerReference(owner, existing, cli.Scheme()); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}
	existing.Rules = desired.Rules
	existing.Labels = desired.Labels
	return cli.Update(ctx, existing)
}

func ensureSecretMigrateRoleBinding(ctx context.Context, cli client.Client, owner *componentApi.AIGateway, infraNs, appNs string) error {
	desired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretMigrateRoleName,
			Namespace: infraNs,
			Labels: map[string]string{
				"app.kubernetes.io/part-of":    "models-as-a-service",
				"app.kubernetes.io/managed-by": "ai-gateway-operator",
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     secretMigrateRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      maasControllerSAName,
				Namespace: appNs,
			},
		},
	}

	existing := &rbacv1.RoleBinding{}
	err := cli.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if k8serr.IsNotFound(err) {
		if err := controllerutil.SetOwnerReference(owner, desired, cli.Scheme()); err != nil {
			return fmt.Errorf("setting owner reference: %w", err)
		}
		return cli.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if err := controllerutil.SetOwnerReference(owner, existing, cli.Scheme()); err != nil {
		return fmt.Errorf("setting owner reference: %w", err)
	}
	existing.Subjects = desired.Subjects
	existing.Labels = desired.Labels
	return cli.Update(ctx, existing)
}
