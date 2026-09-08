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

package operator

import (
	"crypto/tls"
	"testing"

	. "github.com/onsi/gomega"

	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
)

func TestMetricsServerOptions(t *testing.T) {
	tlsOpts := []func(*tls.Config){func(c *tls.Config) { c.MinVersion = tls.VersionTLS12 }}

	t.Run("HTTP skips auth filter and cert dir", func(t *testing.T) {
		g := NewWithT(t)
		opts := metricsServerOptions(&moduleconfig.Config{
			MetricsAddr: ":8080",
		}, tlsOpts)

		g.Expect(opts.BindAddress).Should(Equal(":8080"))
		g.Expect(opts.SecureServing).Should(BeFalse())
		g.Expect(opts.FilterProvider).Should(BeNil())
		g.Expect(opts.CertDir).Should(BeEmpty())
		g.Expect(opts.TLSOpts).Should(HaveLen(1))
	})

	t.Run("HTTPS enables auth filter without requiring a cert dir", func(t *testing.T) {
		g := NewWithT(t)
		opts := metricsServerOptions(&moduleconfig.Config{
			MetricsAddr:     ":8443",
			MetricsSecure:   true,
			MetricsCertPath: "",
		}, tlsOpts)

		g.Expect(opts.SecureServing).Should(BeTrue())
		g.Expect(opts.FilterProvider).ShouldNot(BeNil())
		g.Expect(opts.CertDir).Should(BeEmpty())
	})

	t.Run("HTTPS with cert path sets CertDir", func(t *testing.T) {
		g := NewWithT(t)
		opts := metricsServerOptions(&moduleconfig.Config{
			MetricsAddr:     ":8443",
			MetricsSecure:   true,
			MetricsCertPath: "/tmp/k8s-metrics-server/metrics-certs",
		}, tlsOpts)

		g.Expect(opts.SecureServing).Should(BeTrue())
		g.Expect(opts.FilterProvider).ShouldNot(BeNil())
		g.Expect(opts.CertDir).Should(Equal("/tmp/k8s-metrics-server/metrics-certs"))
	})
}
