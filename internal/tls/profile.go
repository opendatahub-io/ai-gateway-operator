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

package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const bootstrapTimeout = 10 * time.Second

// Result holds the resolved TLS configuration from the cluster profile.
type Result struct {
	Profile          configv1.TLSProfileSpec
	ProfileFetched   bool
	Adherence        configv1.TLSAdherencePolicy
	AdherenceFetched bool
	TLSOpts          []func(*tls.Config)
}

// Resolve fetches the cluster TLS profile and adherence policy, returning a
// Result with TLS options ready for controller-runtime metrics server options.
// On non-OpenShift clusters or transient errors, falls back to Intermediate defaults.
func Resolve(ctx context.Context, k8sClient client.Client, logger logr.Logger) (*Result, error) {
	bootstrapCtx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()

	result := &Result{}

	profile, err := tlspkg.FetchAPIServerTLSProfile(bootstrapCtx, k8sClient)
	if err != nil {
		switch {
		case apimeta.IsNoMatchError(err):
			logger.Info("TLS profile not available (non-OpenShift cluster)")
		case apierrors.IsNotFound(err):
			logger.Info("APIServer resource not found, using defaults")
		case apierrors.IsServiceUnavailable(err),
			apierrors.IsTimeout(err),
			apierrors.IsServerTimeout(err),
			apierrors.IsTooManyRequests(err),
			errors.Is(err, context.DeadlineExceeded):
			// Transient error: mark as fetched so the watcher still registers.
			// The watcher will detect the real profile on first reconcile and
			// trigger a restart if it differs from Intermediate.
			logger.Info("Transient API error, using Intermediate defaults", "error", err)
			result.ProfileFetched = true
		default:
			return nil, fmt.Errorf("reading APIServer TLS profile: %w", err)
		}
		profile = *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	} else {
		result.ProfileFetched = true
	}
	result.Profile = profile

	adherence, adherenceErr := tlspkg.FetchAPIServerTLSAdherencePolicy(bootstrapCtx, k8sClient)
	if adherenceErr != nil {
		switch {
		case apimeta.IsNoMatchError(adherenceErr):
			logger.Info("TLS adherence API not available (non-OpenShift or pre-4.22 cluster)")
		case apierrors.IsNotFound(adherenceErr):
			logger.Info("APIServer resource not found for adherence, skipping")
		case apierrors.IsServiceUnavailable(adherenceErr),
			apierrors.IsTimeout(adherenceErr),
			apierrors.IsServerTimeout(adherenceErr),
			apierrors.IsTooManyRequests(adherenceErr),
			apierrors.IsInternalError(adherenceErr),
			errors.Is(adherenceErr, context.DeadlineExceeded):
			// Transient error: register the watcher anyway so adherence changes are not
			// silently dropped. InitialTLSAdherencePolicy stays zero-value, which causes
			// one extra restart on the first reconcile (acceptable vs. never reloading).
			logger.Info("Transient error fetching TLS adherence policy, watcher will detect change on first reconcile", "error", adherenceErr)
			// Reset the local so the fallback below does not depend on the
			// library's error-path return value.
			adherence = configv1.TLSAdherencePolicyNoOpinion
			result.AdherenceFetched = true
		default:
			return nil, fmt.Errorf("reading APIServer TLS adherence policy: %w", adherenceErr)
		}
	} else {
		result.AdherenceFetched = true
	}
	result.Adherence = adherence

	if adherence == configv1.TLSAdherencePolicyNoOpinion || adherence == configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly {
		logger.Info("using component Intermediate defaults", "adherence", adherence, "clusterProfile", profile.MinTLSVersion)
		profile = *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	}

	minVersion, err := tlsVersion(profile.MinTLSVersion)
	if err != nil {
		return nil, err
	}
	if profile.MinTLSVersion == "" {
		profile.MinTLSVersion = configv1.VersionTLS12
	}

	tlsConfigFn, unsupported := tlspkg.NewTLSConfigFromProfile(profile)
	if minVersion < tls.VersionTLS13 {
		if len(profile.Ciphers) == 0 {
			return nil, fmt.Errorf("TLS profile has no cipher suites for minimum version %s", profile.MinTLSVersion)
		}

		configured := &tls.Config{}
		tlsConfigFn(configured)
		cipherSuites := make([]uint16, 0, len(configured.CipherSuites))
		runtimeSuites := runtimeCipherSuites()
		for _, suite := range configured.CipherSuites {
			if _, ok := runtimeSuites[suite]; ok && (suite < 0x1300 || suite > 0x13ff) {
				cipherSuites = append(cipherSuites, suite)
			} else {
				unsupported = append(unsupported, fmt.Sprintf("0x%04x", suite))
			}
		}
		if len(profile.Ciphers) > 0 && len(cipherSuites) == 0 {
			return nil, fmt.Errorf("TLS profile has no supported cipher suites for minimum version %s", profile.MinTLSVersion)
		}
		result.TLSOpts = append(result.TLSOpts, func(c *tls.Config) {
			tlsConfigFn(c)
			if len(profile.Ciphers) > 0 {
				c.CipherSuites = cipherSuites
			}
		})
	} else {
		result.TLSOpts = append(result.TLSOpts, tlsConfigFn)
	}
	if len(unsupported) > 0 {
		logger.Info("TLS profile contains unsupported ciphers", "unsupported", unsupported)
	}
	result.TLSOpts = append(result.TLSOpts, tlspkg.SetNextProtos(tlspkg.HTTP2NextProtos...))

	return result, nil
}

func tlsVersion(version configv1.TLSProtocolVersion) (uint16, error) {
	switch version {
	case "", configv1.VersionTLS12:
		return tls.VersionTLS12, nil
	case configv1.VersionTLS10:
		return tls.VersionTLS10, nil
	case configv1.VersionTLS11:
		return tls.VersionTLS11, nil
	case configv1.VersionTLS13:
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unknown TLS minimum version %q", version)
	}
}

// runtimeCipherSuites returns the cipher suites this Go build can negotiate,
// restricted to suites an OpenShift TLS profile can name. RC4 suites are
// implemented by the Go runtime (they appear in tls.InsecureCipherSuites) but
// no OpenShift profile contains one and RFC 7465 prohibits them, so they are
// never included in the applied cipher suites.
func runtimeCipherSuites() map[uint16]struct{} {
	suites := make(map[uint16]struct{})
	for _, suite := range tls.CipherSuites() {
		suites[suite.ID] = struct{}{}
	}
	for _, suite := range tls.InsecureCipherSuites() {
		if !strings.Contains(suite.Name, "RC4") {
			suites[suite.ID] = struct{}{}
		}
	}
	return suites
}

// SetupWatcher registers a SecurityProfileWatcher with the manager that triggers
// a graceful restart when the cluster TLS profile or adherence policy changes.
// No-ops when ProfileFetched is false (non-OpenShift or permanent API error).
func SetupWatcher(mgr manager.Manager, result *Result, cancel context.CancelFunc, logger logr.Logger) error {
	if !result.ProfileFetched {
		return nil
	}

	watcher := &tlspkg.SecurityProfileWatcher{
		Client:                mgr.GetClient(),
		InitialTLSProfileSpec: result.Profile,
		OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
			logger.Info("TLS profile changed, initiating shutdown to reload")
			cancel()
		},
	}
	if result.AdherenceFetched {
		watcher.InitialTLSAdherencePolicy = result.Adherence
		watcher.OnAdherencePolicyChange = func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
			logger.Info("TLS adherence policy changed, initiating shutdown to reload")
			cancel()
		}
	}

	if err := watcher.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setting up TLS profile watcher: %w", err)
	}

	return nil
}
