package support

import (
	"context"
	"fmt"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetValidatingWebhookCABundle configures every webhook in configuration to
// trust caBundle.
func SetValidatingWebhookCABundle(
	ctx context.Context,
	cli client.Client,
	key client.ObjectKey,
	caBundle []byte,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		configuration := &admissionregistrationv1.ValidatingWebhookConfiguration{}
		if err := cli.Get(ctx, key, configuration); err != nil {
			return fmt.Errorf("getting ValidatingWebhookConfiguration %s: %w", key.Name, err)
		}

		for i := range configuration.Webhooks {
			configuration.Webhooks[i].ClientConfig.CABundle = caBundle
		}

		if err := cli.Update(ctx, configuration); err != nil {
			return fmt.Errorf("updating ValidatingWebhookConfiguration %s: %w", key.Name, err)
		}

		return nil
	})
}
