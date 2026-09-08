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

package config

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestLoadFromFS_MetricsServing(t *testing.T) {
	t.Run("defaults to HTTP without a cert dir", func(t *testing.T) {
		g := NewWithT(t)

		cfg, err := LoadFromFS(nil)
		g.Expect(err).ShouldNot(HaveOccurred())
		g.Expect(cfg.MetricsSecure).Should(BeFalse())
		g.Expect(cfg.MetricsCertPath).Should(BeEmpty())
		g.Expect(cfg.MetricsAddr).Should(Equal(DefaultMetricsAddr))
	})

	t.Run("env enables HTTPS and cert dir", func(t *testing.T) {
		g := NewWithT(t)
		t.Setenv("ODH_MODULE_OPERATOR_METRICS_SECURE", "true")
		t.Setenv("ODH_MODULE_OPERATOR_METRICS_CERT_PATH", "/tmp/k8s-metrics-server/metrics-certs")
		t.Setenv("ODH_MODULE_OPERATOR_METRICS_BIND_ADDRESS", ":8443")

		cfg, err := LoadFromFS(nil)
		g.Expect(err).ShouldNot(HaveOccurred())
		g.Expect(cfg.MetricsSecure).Should(BeTrue())
		g.Expect(cfg.MetricsCertPath).Should(Equal("/tmp/k8s-metrics-server/metrics-certs"))
		g.Expect(cfg.MetricsAddr).Should(Equal(":8443"))
	})
}
