package e2e

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/ai-gateway-operator/test/support"
)

// Test-owned prerequisites are labelled so setup and cleanup leave externally
// managed resources untouched.
const e2eManagedLabel = "e2e.ai-gateway-operator.io/managed-by"
const e2eManagedValue = "maas-e2e-setup"

// syncMaaSWebhookCABundle supplies the CA injection missing from plain Kind.
// External clusters use OpenShift service-ca or cert-manager instead.
func syncMaaSWebhookCABundle(ctx context.Context, cli client.Client, namespace string, timeout time.Duration) error {
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Name: "maas-controller-webhook-cert", Namespace: namespace}
	if err := cli.Get(ctx, secretKey, secret); err != nil {
		return fmt.Errorf("getting MaaS webhook certificate secret: %w", err)
	}
	if secret.Labels[e2eManagedLabel] != e2eManagedValue {
		return nil
	}
	caBundle := secret.Data["tls.crt"]
	if len(caBundle) == 0 {
		return fmt.Errorf("MaaS webhook certificate secret has no tls.crt")
	}
	webhookKey := client.ObjectKey{Name: "maas-validating-webhook-configuration"}
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, timeout, true, func(ctx context.Context) (bool, error) {
		err := support.SetValidatingWebhookCABundle(ctx, cli, webhookKey, caBundle)
		if k8serr.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	})
}
