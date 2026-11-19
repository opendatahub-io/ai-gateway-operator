package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSyncMaaSWebhookCABundlePreservesManagedCertificates(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "external", true: "test-owned"}[owned], func(t *testing.T) {
			g := NewWithT(t)
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			g.Expect(admissionregistrationv1.AddToScheme(scheme)).To(Succeed())
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "maas-controller-webhook-cert", Namespace: "test"},
				Data:       map[string][]byte{"tls.crt": []byte("serving-cert")},
			}
			if owned {
				secret.Labels = map[string]string{e2eManagedLabel: e2eManagedValue}
			}
			webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: "maas-validating-webhook-configuration"},
				Webhooks: []admissionregistrationv1.ValidatingWebhook{{
					Name:         "vaitenant.kb.io",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{CABundle: []byte("managed-ca")},
				}},
			}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, webhook).Build()
			g.Expect(syncMaaSWebhookCABundle(context.Background(), cli, "test", time.Second)).To(Succeed())
			g.Expect(cli.Get(context.Background(), client.ObjectKeyFromObject(webhook), webhook)).To(Succeed())
			expected := []byte("managed-ca")
			if owned {
				expected = secret.Data["tls.crt"]
			}
			g.Expect(webhook.Webhooks[0].ClientConfig.CABundle).To(Equal(expected))
		})
	}
}
