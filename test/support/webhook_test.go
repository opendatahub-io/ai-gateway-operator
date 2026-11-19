package support

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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

func TestSetValidatingWebhookCABundleRetriesConflict(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(admissionregistrationv1.AddToScheme(scheme)).To(Succeed())
	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "maas-webhook"},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "vaitenant.kb.io"}},
	}
	attempts := 0
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(webhook).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, cli client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			attempts++
			if attempts == 1 {
				return k8serr.NewConflict(schema.GroupResource{Group: admissionregistrationv1.GroupName, Resource: "validatingwebhookconfigurations"}, obj.GetName(), nil)
			}
			return cli.Update(ctx, obj, opts...)
		},
	}).Build()
	g.Expect(SetValidatingWebhookCABundle(context.Background(), cli, client.ObjectKeyFromObject(webhook), []byte("test-ca"))).To(Succeed())
	g.Expect(attempts).To(Equal(2))
	updated := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	g.Expect(cli.Get(context.Background(), client.ObjectKeyFromObject(webhook), updated)).To(Succeed())
	g.Expect(updated.Webhooks[0].ClientConfig.CABundle).To(Equal([]byte("test-ca")))
}
