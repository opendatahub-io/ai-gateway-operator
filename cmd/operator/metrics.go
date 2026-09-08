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

	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
)

// metricsServerOptions mirrors opendatahub-operator: HTTPS metrics and authn/authz
// are opt-in so xKS and local runs can serve plain HTTP when service-ca is absent.
func metricsServerOptions(cfg *moduleconfig.Config, tlsOpts []func(*tls.Config)) metricsserver.Options {
	opts := metricsserver.Options{
		BindAddress:   cfg.MetricsAddr,
		SecureServing: cfg.MetricsSecure,
		TLSOpts:       tlsOpts,
	}
	if cfg.MetricsSecure {
		opts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if cfg.MetricsCertPath != "" {
		opts.CertDir = cfg.MetricsCertPath
	}
	return opts
}
