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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

func init() {
	registerModuleSpec(func(spec *componentsv1alpha1.AIGatewaySpec) {
		spec.ModelsAsAService = componentsv1alpha1.ModelsAsAServiceComponent{
			ManagementState: "Managed",
		}
	})
	registerModuleSetup(maasPrerequisites)
	registerModuleCleanup(maasCleanup)
}

// maasPrerequisites installs the cluster-level prerequisites that the MaaS
// controller manifests require but that a plain kind cluster does not provide.
// Must run before the AIGateway CR is created so the deploy action succeeds.
func maasPrerequisites(ctx context.Context, k8sClient client.Client, ns string) error {
	// 1. Prometheus Operator CRDs — maascontroller/monitoring/podmonitor.yaml is
	//    rendered and applied by AGO's deploy action. Without these CRDs the REST
	//    mapper returns "no matches for kind PodMonitor" and the action fails.
	if err := ensurePrometheusCRDs(ctx, k8sClient); err != nil {
		return fmt.Errorf("installing Prometheus CRDs: %w", err)
	}

	// 2. Webhook cert secret — maas-controller Deployment mounts this volume.
	//    Without it the pod cannot start. cert-manager is not present in kind,
	//    so we create a self-signed TLS secret directly.
	//    CA bundle injection into the ValidatingWebhookConfiguration is not
	//    needed: the three webhooks cover only aitenants/maassubscriptions/
	//    maasauthpolicies — none of which are created by our E2E tests.
	if err := ensureWebhookCertSecret(ctx, k8sClient, ns); err != nil {
		return fmt.Errorf("creating webhook cert secret: %w", err)
	}

	// 3. Optional CRD stubs — maas-controller watches authpolicies and
	//    tokenratelimitpolicies (kuadrant.io) and llminferenceservices
	//    (serving.kserve.io). When these CRDs do not exist, controller-runtime
	//    cannot start the informers and crashes the whole process after ~30s.
	//    Installing empty stub CRDs lets the informers sync to empty lists.
	if err := ensureOptionalCRDStubs(ctx, k8sClient); err != nil {
		return fmt.Errorf("installing optional CRD stubs: %w", err)
	}

	return nil
}

// ensurePrometheusCRDs creates minimal monitoring.coreos.com/v1 CRDs so that
// the REST mapper can resolve PodMonitor, ServiceMonitor and PrometheusRule.
func ensurePrometheusCRDs(ctx context.Context, k8sClient client.Client) error {
	crds := []apiextensionsv1.CustomResourceDefinition{
		minimalCRD("podmonitors.monitoring.coreos.com", "monitoring.coreos.com", "v1",
			"PodMonitor", "podmonitors", "podmonitor", apiextensionsv1.NamespaceScoped),
		minimalCRD("servicemonitors.monitoring.coreos.com", "monitoring.coreos.com", "v1",
			"ServiceMonitor", "servicemonitors", "servicemonitor", apiextensionsv1.NamespaceScoped),
		minimalCRD("prometheusrules.monitoring.coreos.com", "monitoring.coreos.com", "v1",
			"PrometheusRule", "prometheusrules", "prometheusrule", apiextensionsv1.NamespaceScoped),
	}
	return applyCRDs(ctx, k8sClient, crds)
}

// ensureOptionalCRDStubs creates minimal stubs for CRDs that maas-controller
// watches optionally. Without them controller-runtime times out syncing the
// informer cache and crashes the pod.
func ensureOptionalCRDStubs(ctx context.Context, k8sClient client.Client) error {
	crds := []apiextensionsv1.CustomResourceDefinition{
		minimalCRD("authpolicies.kuadrant.io", "kuadrant.io", "v1beta2",
			"AuthPolicy", "authpolicies", "authpolicy", apiextensionsv1.NamespaceScoped),
		minimalCRD("tokenratelimitpolicies.kuadrant.io", "kuadrant.io", "v1beta3",
			"TokenRateLimitPolicy", "tokenratelimitpolicies", "tokenratelimitpolicy", apiextensionsv1.NamespaceScoped),
		minimalCRD("llminferenceservices.serving.kserve.io", "serving.kserve.io", "v1alpha1",
			"LLMInferenceService", "llminferenceservices", "llminferenceservice", apiextensionsv1.NamespaceScoped),
	}
	return applyCRDs(ctx, k8sClient, crds)
}

// e2eManagedLabel is added to every CRD created by the test setup so that
// cleanup can distinguish test-owned stubs from pre-existing cluster CRDs
// (e.g. Prometheus CRDs on ROSA/OpenShift). Only CRDs carrying this label
// are deleted during teardown.
const e2eManagedLabel = "e2e.ai-gateway-operator.io/managed-by"
const e2eManagedValue = "maas-e2e-setup"

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

// minimalCRD builds a CRD with open schema (x-preserve-unknown-fields) so any
// resource of that kind is accepted without validation. Used for test stubs only.
func minimalCRD(name, group, version, kind, plural, singular string, scope apiextensionsv1.ResourceScope) apiextensionsv1.CustomResourceDefinition {
	return apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{e2eManagedLabel: e2eManagedValue},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    version,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type:                   "object",
						XPreserveUnknownFields: boolPtr(true),
					},
				},
			}},
			Scope: scope,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: singular,
				Kind:     kind,
			},
		},
	}
}

// applyCRDs creates each CRD if it does not already exist, then waits
// for it to become Established before returning.
func applyCRDs(ctx context.Context, k8sClient client.Client, crds []apiextensionsv1.CustomResourceDefinition) error {
	for i := range crds {
		err := k8sClient.Create(ctx, &crds[i])
		if err != nil && !k8serr.IsAlreadyExists(err) {
			return fmt.Errorf("creating CRD %s: %w", crds[i].Name, err)
		}
		if err := waitForCRDEstablished(ctx, k8sClient, crds[i].Name); err != nil {
			return fmt.Errorf("waiting for CRD %s to establish: %w", crds[i].Name, err)
		}
	}
	return nil
}

// waitForCRDEstablished polls until the CRD reaches Established condition.
func waitForCRDEstablished(ctx context.Context, k8sClient client.Client, crdName string) error {
	deadline := time.Now().Add(setupTimeout)
	for time.Now().Before(deadline) {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, crd); err != nil {
			return fmt.Errorf("getting CRD: %w", err)
		}
		for _, cond := range crd.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("CRD %s did not become Established within %v", crdName, setupTimeout)
}

// ensureWebhookCertSecret creates a self-signed TLS secret so the
// maas-controller pod can mount its webhook-cert volume and start.
// The secret is marked with e2eManagedLabel so cleanup can identify
// test-created secrets and avoid deleting pre-existing ones.
func ensureWebhookCertSecret(ctx context.Context, k8sClient client.Client, ns string) error {
	const secretName = "maas-controller-webhook-cert"

	existing := &corev1.Secret{}
	err := k8sClient.Get(ctx, client.ObjectKey{Name: secretName, Namespace: ns}, existing)
	if err == nil {
		// Secret already exists — only take ownership if it's not already marked
		if existing.Labels[e2eManagedLabel] != e2eManagedValue {
			return nil // pre-existing secret, leave it alone
		}
		return nil // already created by us
	}
	if !k8serr.IsNotFound(err) {
		return err
	}

	certPEM, keyPEM, err := generateSelfSignedCert(ns)
	if err != nil {
		return fmt.Errorf("generating TLS cert: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ns,
			Labels:    map[string]string{e2eManagedLabel: e2eManagedValue},
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}
	return k8sClient.Create(ctx, secret)
}

// generateSelfSignedCert returns a PEM-encoded TLS cert and key pair for the
// maas-controller webhook service. The cert is self-signed and intended only
// for E2E test clusters where cert-manager is not available.
func generateSelfSignedCert(ns string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	// Use a cryptographically random serial number to avoid collisions if the
	// test runs multiple times on the same cluster.
	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		return nil, nil, fmt.Errorf("generating serial number: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: fmt.Sprintf("maas-controller-webhook-service.%s.svc", ns)},
		DNSNames: []string{
			fmt.Sprintf("maas-controller-webhook-service.%s.svc", ns),
			fmt.Sprintf("maas-controller-webhook-service.%s.svc.cluster.local", ns),
		},
		NotBefore:             now.Add(-1 * time.Minute), // 1 min buffer for clock skew
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

func TestModelsAsAService(t *testing.T) {
	maasControllerDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "maas-controller",
			Namespace: operatorNamespace,
		},
	}

	t.Run("should deploy maas-controller", func(t *testing.T) {
		eventuallyDeploymentReady(t, maasControllerDeploy)
	})
	t.Run("should create MaaS CRDs", func(t *testing.T) {
		testMaaSCRDsCreated(t)
	})
	t.Run("should create maas-parameters ConfigMap", func(t *testing.T) {
		testMaaSConfigMapCreated(t)
	})
	t.Run("should create default Tenant CR", func(t *testing.T) {
		testDefaultTenantCreated(t)
	})
	t.Run("should set platform labels on maas-controller", func(t *testing.T) {
		testMaaSControllerPlatformLabels(t, maasControllerDeploy)
	})
	t.Run("should set owner references on maas-controller", func(t *testing.T) {
		testMaaSControllerOwnerReferences(t, maasControllerDeploy)
	})
}

func testMaaSCRDsCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	expectedCRDs := []string{
		"aitenants.maas.opendatahub.io",
		"configs.maas.opendatahub.io",
		"externalmodels.maas.opendatahub.io",
		"maasauthpolicies.maas.opendatahub.io",
		"maasmodelrefs.maas.opendatahub.io",
		"maassubscriptions.maas.opendatahub.io",
		"tenants.maas.opendatahub.io",
		"externalmodels.inference.opendatahub.io",
		"externalproviders.inference.opendatahub.io",
	}

	for _, crdName := range expectedCRDs {
		crd := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: crdName},
		}
		g.Eventually(k.Get(crd)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
			jq.Match(`.metadata.name == "%s"`, crdName),
		)
	}
}

func testMaaSConfigMapCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "maas-parameters",
			Namespace: operatorNamespace,
		},
	}

	g.Eventually(k.Get(configMap)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.data."maas-controller-image" != ""`),
		jq.Match(`.data."maas-api-image" != ""`),
		jq.Match(`.data."maas-api-key-cleanup-image" != ""`),
		jq.Match(`.data."monitoring-namespace" != ""`),
	))
}

func testDefaultTenantCreated(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	// The default Tenant CR is created in the operator's namespace
	// We need to use unstructured client because Tenant CRD type is not in our scheme
	tenant := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "maas.opendatahub.io/v1alpha1",
			Kind:       "Tenant",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-tenant",
			Namespace: operatorNamespace,
		},
	}

	g.Eventually(func(g Gomega) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(tenant.GetName()).To(Equal("default-tenant"))
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func testMaaSControllerPlatformLabels(t *testing.T, maasControllerDeploy *appsv1.Deployment) {
	t.Helper()
	g := NewWithT(t)

	fresh := module.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(fresh), fresh)).To(Succeed())

	operatorCfg := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorConfigMapName,
			Namespace: support.OperatorNamespace(),
		},
	}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(operatorCfg), operatorCfg)).To(Succeed())

	g.Eventually(k.Get(maasControllerDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.labels."%s" == "aigateway"`, labelPartOf),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceName,
			fresh.GetName()),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceUID,
			string(fresh.GetUID())),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationType,
			operatorCfg.Data[moduleconfig.KeyPlatformType]),
	))
}

func testMaaSControllerOwnerReferences(t *testing.T, maasControllerDeploy *appsv1.Deployment) {
	t.Helper()
	g := NewWithT(t)

	g.Eventually(k.Get(maasControllerDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "AIGateway") | .name == "%s"`,
			componentsv1alpha1.AIGatewayInstanceName),
	)
}

// maasCleanup removes stub CRDs and the webhook cert secret created by
// maasPrerequisites. Best-effort — errors are logged but do not fail the test.
// Only CRDs carrying the e2eManagedLabel are deleted; pre-existing cluster CRDs
// (e.g. Prometheus CRDs on ROSA/OpenShift) are left untouched.
// Only secrets marked with e2eManagedLabel are deleted; pre-existing secrets
// are left untouched.
func maasCleanup(ctx context.Context, k8sClient client.Client, ns string) {
	stubCRDNames := []string{
		"podmonitors.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"authpolicies.kuadrant.io",
		"tokenratelimitpolicies.kuadrant.io",
		"llminferenceservices.serving.kserve.io",
	}
	for _, name := range stubCRDNames {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Name: name}, crd); err != nil {
			if !k8serr.IsNotFound(err) {
				fmt.Printf("maasCleanup: failed to get CRD %s: %v\n", name, err)
			}
			continue // not found or error — nothing to clean up
		}
		if crd.Labels[e2eManagedLabel] != e2eManagedValue {
			continue // pre-existing CRD — do not touch
		}
		if err := k8sClient.Delete(ctx, crd); err != nil && !k8serr.IsNotFound(err) {
			fmt.Printf("maasCleanup: failed to delete CRD %s: %v\n", name, err)
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "maas-controller-webhook-cert", Namespace: ns},
	}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: secret.Name, Namespace: ns}, secret); err != nil {
		if !k8serr.IsNotFound(err) {
			fmt.Printf("maasCleanup: failed to get webhook cert secret: %v\n", err)
		}
		return // not found or error — nothing to clean up
	}
	if secret.Labels[e2eManagedLabel] != e2eManagedValue {
		return // pre-existing secret — do not touch
	}
	if err := k8sClient.Delete(ctx, secret); err != nil && !k8serr.IsNotFound(err) {
		fmt.Printf("maasCleanup: failed to delete webhook cert secret: %v\n", err)
	}
}
