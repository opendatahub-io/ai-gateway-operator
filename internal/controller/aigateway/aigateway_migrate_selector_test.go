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
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

func createMaasControllerDeployment(namespace string, selectorLabels map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      maasControllerDeploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: selectorLabels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: "test"}}},
			},
		},
	}
}

func TestMigrateSelector(t *testing.T) {
	t.Run("should delegate to migrateMaasControllerSelector", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		dep := createMaasControllerDeployment(odhApplicationsNS, map[string]string{"control-plane": "maas-controller"})

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		err := cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "stale Deployment should have been deleted via the delegated call")
	})
}

func TestMigrateMaasControllerSelector(t *testing.T) {
	t.Run("should be no-op when Deployment does not exist", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())
	})

	t.Run("should delete Deployment with stale modelsasservice selector labels", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		staleSelector := map[string]string{
			"control-plane":                      "maas-controller",
			"app.kubernetes.io/part-of":          "modelsasservice",
			"app.opendatahub.io/modelsasservice": "true",
		}
		dep := createMaasControllerDeployment(odhApplicationsNS, staleSelector)

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		err := cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)
		g.Expect(err).Should(HaveOccurred())
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
	})

	t.Run("should delete Deployment missing the required labels entirely", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		dep := createMaasControllerDeployment(odhApplicationsNS, map[string]string{"control-plane": "maas-controller"})

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		err := cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
	})

	t.Run("should delete Deployment with nil selector", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		dep := createMaasControllerDeployment(odhApplicationsNS, nil)
		dep.Spec.Selector = nil

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		err := cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
	})

	t.Run("should not delete Deployment already carrying the current aigateway selector labels", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		currentSelector := map[string]string{
			"control-plane":                "maas-controller",
			"app.kubernetes.io/part-of":    componentName,
			"app.opendatahub.io/aigateway": "true",
		}
		dep := createMaasControllerDeployment(odhApplicationsNS, currentSelector)

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		g.Expect(cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)).To(Succeed())
	})

	t.Run("should not delete Deployment with the current labels plus other selector entries", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		selectorWithExtra := map[string]string{
			"control-plane":                "maas-controller",
			"app.kubernetes.io/part-of":    componentName,
			"app.opendatahub.io/aigateway": "true",
			"app.kubernetes.io/component":  "models-as-a-service",
		}
		dep := createMaasControllerDeployment(odhApplicationsNS, selectorWithExtra)

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		g.Expect(cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)).To(Succeed())
	})

	t.Run("should delete Deployment carrying the current labels plus the legacy modelsasservice key", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		// app.opendatahub.io/modelsasservice is a distinct key from
		// app.opendatahub.io/aigateway, so both can coexist in the same selector
		// without a key collision (unlike app.kubernetes.io/part-of, which can only
		// hold one value at a time). A selector like this could result from a
		// Deployment recreated or patched with a merged selector; it must still be
		// treated as stale, otherwise deploy.NewAction would fail trying to apply a
		// selector that drops the legacy key.
		mergedSelector := map[string]string{
			"control-plane":                      "maas-controller",
			"app.kubernetes.io/part-of":          componentName,
			"app.opendatahub.io/aigateway":       "true",
			"app.opendatahub.io/modelsasservice": "true",
		}
		dep := createMaasControllerDeployment(odhApplicationsNS, mergedSelector)

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).Build()
		rr := &odhtypes.ReconciliationRequest{Client: cli}

		g.Expect(m.migrateMaasControllerSelector(ctx, rr)).To(Succeed())

		result := &appsv1.Deployment{}
		err := cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue(), "Deployment carrying a known-legacy selector key should still be deleted for recreation")
	})

	t.Run("should error when the client is nil", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		rr := &odhtypes.ReconciliationRequest{}

		err := m.migrateMaasControllerSelector(ctx, rr)
		g.Expect(err).Should(HaveOccurred())
	})

	t.Run("should not error and must not delete the object when a Get/Delete race changes it (CWE-367)", func(t *testing.T) {
		g := NewWithT(t)
		ctx := t.Context()

		m := newTestModuleWithNamespace(t, odhApplicationsNS)
		dep := createMaasControllerDeployment(odhApplicationsNS, map[string]string{"control-plane": "maas-controller"})

		cli := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(dep).
			WithInterceptorFuncs(interceptor.Funcs{
				// Simulate a concurrent recreate/update landing between the Get
				// inside migrateMaasControllerSelector and its later Delete call:
				// bump the stored object's ResourceVersion right after the Get
				// returns, so the caller's in-memory copy (and the Delete
				// precondition built from it) is stale by the time Delete runs.
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := c.Get(ctx, key, obj, opts...); err != nil {
						return err
					}
					live := obj.(*appsv1.Deployment).DeepCopy() //nolint:forcetypeassert
					if live.Annotations == nil {
						live.Annotations = map[string]string{}
					}
					live.Annotations["race-simulated"] = "true"
					return c.Update(ctx, live)
				},
			}).
			Build()

		rr := &odhtypes.ReconciliationRequest{Client: cli}

		err := m.migrateMaasControllerSelector(ctx, rr)
		g.Expect(err).NotTo(HaveOccurred(), "a benign Conflict from the Delete precondition must not surface as a reconcile error")

		// The object that won the race must survive: an unguarded Delete would
		// have removed it even though it is no longer the object we read.
		result := &appsv1.Deployment{}
		g.Expect(cli.Get(ctx, client.ObjectKey{Name: maasControllerDeploymentName, Namespace: odhApplicationsNS}, result)).To(Succeed())
		g.Expect(result.Annotations).To(HaveKeyWithValue("race-simulated", "true"))
	})
}
