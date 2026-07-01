//go:build e2e

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

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"
	k8sm "github.com/lburgazzoli/gomega-matchers/pkg/matchers/k8s"

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

const (
	timeout  = 90 * time.Second
	interval = 2 * time.Second

	labelPartOf            = "platform.opendatahub.io/part-of"
	annotationInstanceName = "platform.opendatahub.io/instance.name"
	annotationInstanceUID  = "platform.opendatahub.io/instance.uid"
	annotationType         = "platform.opendatahub.io/type"

	operatorConfigMapName = "ai-gateway-config"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
	k         *k8sm.Matcher

	testScheme = runtime.NewScheme()

	module            *componentsv1alpha1.AIGateway
	operatorNamespace string

	moduleSpecFns []func(*componentsv1alpha1.AIGatewaySpec)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(testScheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(testScheme))
}

// registerModuleSpec lets each component test file contribute its spec
// fields via init(), so adding a new component never touches existing files.
func registerModuleSpec(fn func(*componentsv1alpha1.AIGatewaySpec)) {
	moduleSpecFns = append(moduleSpecFns, fn)
}

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) int {
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	operatorNamespace = support.OperatorNamespace()

	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get kubeconfig: %v\n", err)
		return 1
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		return 1
	}

	k = k8sm.New(k8sClient, testScheme)

	if err := pollFor(ctx, "operator deployment ready", func() (bool, error) {
		deploy := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKey{
			Name: "ai-gateway-operator", Namespace: operatorNamespace,
		}, deploy); err != nil {
			return false, nil
		}
		return deploy.Status.ReadyReplicas >= 1, nil
	}); err != nil {
		return 1
	}

	module = &componentsv1alpha1.AIGateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentsv1alpha1.AIGatewayInstanceName,
		},
	}
	for _, fn := range moduleSpecFns {
		fn(&module.Spec)
	}

	_ = k8sClient.Delete(ctx, module)
	if err := pollFor(ctx, "module CR deleted", func() (bool, error) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(module), module.DeepCopy())
		return err != nil, nil
	}); err != nil {
		return 1
	}

	module.ResourceVersion = ""
	module.UID = ""
	if err := k8sClient.Create(ctx, module); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create AIGateway module: %v\n", err)
		return 1
	}

	if err := pollFor(ctx, "module CR ready", func() (bool, error) {
		fresh := module.DeepCopy()
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(module), fresh); err != nil {
			return false, nil
		}
		for _, c := range fresh.Status.Conditions {
			if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
				*module = *fresh
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return 1
	}

	code := m.Run()

	_ = k8sClient.Delete(ctx, module)

	return code
}

func pollFor(ctx context.Context, desc string, fn func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		done, err := fn()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error polling for %s: %v\n", desc, err)
			return err
		}
		if done {
			return nil
		}
		time.Sleep(interval)
	}
	fmt.Fprintf(os.Stderr, "Timed out waiting for %s\n", desc)
	return fmt.Errorf("timed out waiting for %s", desc)
}

func waitForDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(func(g Gomega) {
		fresh := obj.DeepCopyObject().(client.Object)
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), fresh)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func waitForSingletonDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	waitForDeleted(t, obj)
	obj.SetResourceVersion("")
	obj.SetUID("")
}

func eventuallyDeploymentReady(t *testing.T, deploy *appsv1.Deployment) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(k.Get(deploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.status.readyReplicas >= 1`),
	)
}
