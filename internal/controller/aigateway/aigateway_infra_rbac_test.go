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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEnsureNamespace_CreatesWithAllLabels(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	g.Expect(ensureNamespace(context.Background(), cli, "test-infra-ns")).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cli.Get(context.Background(), types.NamespacedName{Name: "test-infra-ns"}, ns)).To(Succeed())
	g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/part-of", "ai-gateway"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "ai-gateway-operator"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("opendatahub.io/generated-namespace", "true"))
}

func TestEnsureNamespace_PatchesLabelOnExistingNamespace(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-infra-ns",
			Labels: map[string]string{
				"some-other-label": "value",
			},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	g.Expect(ensureNamespace(context.Background(), cli, "test-infra-ns")).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cli.Get(context.Background(), types.NamespacedName{Name: "test-infra-ns"}, ns)).To(Succeed())
	g.Expect(ns.Labels).To(HaveKeyWithValue("opendatahub.io/generated-namespace", "true"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/part-of", "ai-gateway"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "ai-gateway-operator"))
	g.Expect(ns.Labels).To(HaveKeyWithValue("some-other-label", "value"))
}

func TestEnsureNamespace_NoUpdateWhenLabelsPresent(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-infra-ns",
			Labels: map[string]string{
				"app.kubernetes.io/part-of":          "ai-gateway",
				"app.kubernetes.io/managed-by":       "ai-gateway-operator",
				"opendatahub.io/generated-namespace": "true",
			},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	g.Expect(ensureNamespace(context.Background(), cli, "test-infra-ns")).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cli.Get(context.Background(), types.NamespacedName{Name: "test-infra-ns"}, ns)).To(Succeed())
	g.Expect(ns.Labels).To(HaveLen(3))
}

func TestEnsureNamespace_PatchesNilLabels(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-infra-ns",
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	g.Expect(ensureNamespace(context.Background(), cli, "test-infra-ns")).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cli.Get(context.Background(), types.NamespacedName{Name: "test-infra-ns"}, ns)).To(Succeed())
	g.Expect(ns.Labels).To(HaveKeyWithValue("opendatahub.io/generated-namespace", "true"))
}
