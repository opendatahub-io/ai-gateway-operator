package support

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSetValidatingWebhookCABundleSetsBundleForEachWebhook(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(admissionregistrationv1.AddToScheme(scheme)).To(Succeed())

	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "maas-validating-webhook-configuration"},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "vaitenant.kb.io"},
			{Name: "vmaassubscription.kb.io"},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(webhook).Build()
	caBundle := []byte("test-ca")

	g.Expect(SetValidatingWebhookCABundle(context.Background(), cli, client.ObjectKeyFromObject(webhook), caBundle)).To(Succeed())

	updated := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	g.Expect(cli.Get(context.Background(), client.ObjectKeyFromObject(webhook), updated)).To(Succeed())
	for _, configuredWebhook := range updated.Webhooks {
		g.Expect(configuredWebhook.ClientConfig.CABundle).To(Equal(caBundle))
	}
}
