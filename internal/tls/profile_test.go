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
	cryptotls "crypto/tls"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestResolveTLSProfile(t *testing.T) {
	t.Run("uses the Intermediate profile when the cluster profile is nil", func(t *testing.T) {
		scheme := runtime.NewScheme()
		NewWithT(t).Expect(configv1.Install(scheme)).To(Succeed())
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&configv1.APIServer{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		}).Build()

		result, err := Resolve(context.Background(), client, logr.Discard())
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.Profile).To(Equal(*configv1.TLSProfiles[configv1.TLSProfileIntermediateType]))
		g.Expect(applyTLSOptions(result.TLSOpts).MinVersion).To(Equal(uint16(cryptotls.VersionTLS12)))
	})

	t.Run("strict adherence applies supported custom cipher suites", func(t *testing.T) {
		profile := configv1.TLSProfileSpec{
			MinTLSVersion: configv1.VersionTLS12,
			Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
			Groups:        []configv1.TLSGroup{configv1.TLSGroupSecP256r1},
		}
		result, err := resolveProfile(t, profile, configv1.TLSAdherencePolicyStrictAllComponents)
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())

		config := applyTLSOptions(result.TLSOpts)
		g.Expect(config.MinVersion).To(Equal(uint16(cryptotls.VersionTLS12)))
		g.Expect(config.CipherSuites).To(Equal([]uint16{cryptotls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}))
		g.Expect(config.CurvePreferences).To(Equal([]cryptotls.CurveID{cryptotls.CurveP256}))
		g.Expect(config.NextProtos).To(Equal([]string{"h2", "http/1.1"}))
	})

	t.Run("strict adherence rejects TLS 1.2 profiles with an empty cipher list", func(t *testing.T) {
		for _, adherence := range []configv1.TLSAdherencePolicy{
			configv1.TLSAdherencePolicyStrictAllComponents,
			"future-policy",
		} {
			t.Run(string(adherence), func(t *testing.T) {
				_, err := resolveProfile(t, configv1.TLSProfileSpec{
					MinTLSVersion: configv1.VersionTLS12,
				}, adherence)
				g := NewWithT(t)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("no cipher suites"))
			})
		}
	})

	t.Run("rejects a TLS 1.2 profile with no runtime-supported cipher suites", func(t *testing.T) {
		_, err := resolveProfile(t, configv1.TLSProfileSpec{
			MinTLSVersion: configv1.VersionTLS12,
			Ciphers: []string{
				"TLS_AES_128_GCM_SHA256",
				"ECDHE-ECDSA-AES256-SHA384",
			},
		}, configv1.TLSAdherencePolicyStrictAllComponents)

		g := NewWithT(t)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("no supported cipher suites"))
	})

	t.Run("NoOpinion uses the component Intermediate defaults instead of the cluster profile", func(t *testing.T) {
		assertComponentDefaults(t, configv1.TLSAdherencePolicyNoOpinion)
	})

	t.Run("Legacy uses the component Intermediate defaults instead of the cluster profile", func(t *testing.T) {
		assertComponentDefaults(t, configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly)
	})

	t.Run("rejects unknown minimum versions without panicking", func(t *testing.T) {
		_, err := resolveProfile(t, configv1.TLSProfileSpec{
			MinTLSVersion: "VersionTLS99",
			Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
		}, configv1.TLSAdherencePolicyStrictAllComponents)

		g := NewWithT(t)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unknown TLS minimum version"))
	})

	t.Run("keeps the supported subset of mixed cipher requests", func(t *testing.T) {
		result, err := resolveProfile(t, configv1.TLSProfileSpec{
			MinTLSVersion: configv1.VersionTLS12,
			Ciphers: []string{
				"TLS_AES_128_GCM_SHA256",
				"ECDHE-ECDSA-AES256-SHA384",
				"ECDHE-RSA-AES128-GCM-SHA256",
			},
		}, configv1.TLSAdherencePolicyStrictAllComponents)
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(applyTLSOptions(result.TLSOpts).CipherSuites).To(Equal([]uint16{cryptotls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}))
	})

	t.Run("leaves TLS 1.3 cipher selection Go-managed", func(t *testing.T) {
		result, err := resolveProfile(t, *configv1.TLSProfiles[configv1.TLSProfileModernType], configv1.TLSAdherencePolicyStrictAllComponents)
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())

		config := applyTLSOptions(result.TLSOpts)
		g.Expect(config.MinVersion).To(Equal(uint16(cryptotls.VersionTLS13)))
		g.Expect(config.CipherSuites).To(BeNil())
	})

	t.Run("defaults an empty minimum version to TLS 1.2", func(t *testing.T) {
		result, err := resolveProfile(t, configv1.TLSProfileSpec{
			Ciphers: []string{"ECDHE-RSA-AES128-GCM-SHA256"},
		}, configv1.TLSAdherencePolicyStrictAllComponents)
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())
		cfg := applyTLSOptions(result.TLSOpts)
		g.Expect(cfg.MinVersion).To(Equal(uint16(cryptotls.VersionTLS12)))
		g.Expect(cfg.CipherSuites).To(Equal([]uint16{cryptotls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}))
	})

	t.Run("honors the Old profile exactly under Strict adherence", func(t *testing.T) {
		result, err := resolveProfile(t, *configv1.TLSProfiles[configv1.TLSProfileOldType], configv1.TLSAdherencePolicyStrictAllComponents)
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())

		config := applyTLSOptions(result.TLSOpts)
		g.Expect(result.Profile).To(Equal(*configv1.TLSProfiles[configv1.TLSProfileOldType]))
		g.Expect(config.MinVersion).To(Equal(uint16(cryptotls.VersionTLS10)))
		g.Expect(config.CipherSuites).To(ContainElement(cryptotls.TLS_RSA_WITH_3DES_EDE_CBC_SHA))
	})

	t.Run("denied and unexpected APIServer reads fail resolution instead of falling back", func(t *testing.T) {
		apiServerGR := schema.GroupResource{Group: "config.openshift.io", Resource: "apiservers"}
		testcases := []struct {
			name        string
			failOnCall  int
			readErr     error
			wantErrText string
		}{
			{
				name:        "forbidden profile read",
				failOnCall:  1,
				readErr:     apierrors.NewForbidden(apiServerGR, "cluster", nil),
				wantErrText: "reading APIServer TLS profile",
			},
			{
				name:        "unexpected profile read error",
				failOnCall:  1,
				readErr:     apierrors.NewBadRequest("corrupted response"),
				wantErrText: "reading APIServer TLS profile",
			},
			{
				name:        "forbidden adherence read",
				failOnCall:  2,
				readErr:     apierrors.NewForbidden(apiServerGR, "cluster", nil),
				wantErrText: "reading APIServer TLS adherence policy",
			},
			{
				name:        "unexpected adherence read error",
				failOnCall:  2,
				readErr:     apierrors.NewBadRequest("corrupted response"),
				wantErrText: "reading APIServer TLS adherence policy",
			},
		}
		for _, tc := range testcases {
			t.Run(tc.name, func(t *testing.T) {
				scheme := runtime.NewScheme()
				NewWithT(t).Expect(configv1.Install(scheme)).To(Succeed())
				getCalls := 0
				client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&configv1.APIServer{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Spec: configv1.APIServerSpec{
						TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
						TLSAdherence:       configv1.TLSAdherencePolicyStrictAllComponents,
					},
				}).WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						getCalls++
						if getCalls == tc.failOnCall {
							return tc.readErr
						}
						return c.Get(ctx, key, obj, opts...)
					},
				}).Build()

				result, err := Resolve(context.Background(), client, logr.Discard())
				g := NewWithT(t)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.wantErrText))
				g.Expect(result).To(BeNil())
			})
		}
	})

	t.Run("transient adherence errors fall back to Intermediate defaults", func(t *testing.T) {
		scheme := runtime.NewScheme()
		NewWithT(t).Expect(configv1.Install(scheme)).To(Succeed())
		client := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
				return apierrors.NewServiceUnavailable("apiserver unavailable")
			},
		}).Build()

		result, err := Resolve(context.Background(), client, logr.Discard())
		g := NewWithT(t)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result.ProfileFetched).To(BeTrue())
		g.Expect(result.AdherenceFetched).To(BeTrue())
		g.Expect(result.Adherence).To(Equal(configv1.TLSAdherencePolicyNoOpinion))
		g.Expect(result.Profile).To(Equal(*configv1.TLSProfiles[configv1.TLSProfileIntermediateType]))
		g.Expect(applyTLSOptions(result.TLSOpts).MinVersion).To(Equal(uint16(cryptotls.VersionTLS12)))
	})
}

func assertComponentDefaults(t *testing.T, adherence configv1.TLSAdherencePolicy) {
	t.Helper()
	result, err := resolveProfile(t, configv1.TLSProfileSpec{
		MinTLSVersion: configv1.VersionTLS12,
		Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
		Groups:        []configv1.TLSGroup{configv1.TLSGroupSecP384r1},
	}, adherence)
	g := NewWithT(t)
	g.Expect(err).NotTo(HaveOccurred())

	got := applyTLSOptions(result.TLSOpts)
	g.Expect(result.Profile.MinTLSVersion).To(Equal(configv1.VersionTLS12))
	expected := &cryptotls.Config{}
	defaultProfile := *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	defaultTLSOpts, _ := tlspkg.NewTLSConfigFromProfile(defaultProfile)
	defaultTLSOpts(expected)
	filteredCiphers := make([]uint16, 0, len(expected.CipherSuites))
	for _, suite := range expected.CipherSuites {
		if suite < 0x1300 || suite > 0x13ff {
			filteredCiphers = append(filteredCiphers, suite)
		}
	}
	expected.CipherSuites = filteredCiphers
	tlspkg.SetNextProtos(tlspkg.HTTP2NextProtos...)(expected)
	g.Expect(got.MinVersion).To(Equal(expected.MinVersion))
	g.Expect(got.CipherSuites).To(Equal(expected.CipherSuites))
	g.Expect(got.CurvePreferences).To(Equal(expected.CurvePreferences))
	g.Expect(got.NextProtos).To(Equal(expected.NextProtos))
}

func resolveProfile(t *testing.T, profile configv1.TLSProfileSpec, adherence configv1.TLSAdherencePolicy) (*Result, error) {
	t.Helper()
	scheme := runtime.NewScheme()
	NewWithT(t).Expect(configv1.Install(scheme)).To(Succeed())
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{TLSProfileSpec: profile},
			},
			TLSAdherence: adherence,
		},
	}).Build()
	return Resolve(context.Background(), client, logr.Discard())
}

func applyTLSOptions(options []func(*cryptotls.Config)) *cryptotls.Config {
	config := &cryptotls.Config{}
	for _, option := range options {
		option(config)
	}
	return config
}

func TestResolveTLSProfileDropsRC4Ciphers(t *testing.T) {
	// A mixed list keeps its usable subset with the RC4 entry dropped.
	result, err := resolveProfile(t, configv1.TLSProfileSpec{
		MinTLSVersion: configv1.VersionTLS12,
		Ciphers:       []string{"TLS_RSA_WITH_RC4_128_SHA", "ECDHE-RSA-AES128-GCM-SHA256"},
	}, configv1.TLSAdherencePolicyStrictAllComponents)
	g := NewWithT(t)
	g.Expect(err).NotTo(HaveOccurred())
	got := applyTLSOptions(result.TLSOpts)
	g.Expect(got.CipherSuites).To(Equal([]uint16{cryptotls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}))

	// An all-RC4 list has no usable suites and fails closed.
	_, err = resolveProfile(t, configv1.TLSProfileSpec{
		MinTLSVersion: configv1.VersionTLS12,
		Ciphers:       []string{"TLS_RSA_WITH_RC4_128_SHA"},
	}, configv1.TLSAdherencePolicyStrictAllComponents)
	g = NewWithT(t)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("no supported cipher suites"))
}

func TestResolveTLSProfileRetainsImplementedCustomCiphers(t *testing.T) {
	// Custom profiles may request any cipher implemented by the runtime.
	result, err := resolveProfile(t, configv1.TLSProfileSpec{
		MinTLSVersion: configv1.VersionTLS12,
		Ciphers:       []string{"TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA", "ECDHE-RSA-AES128-GCM-SHA256"},
	}, configv1.TLSAdherencePolicyStrictAllComponents)
	g := NewWithT(t)
	g.Expect(err).NotTo(HaveOccurred())
	got := applyTLSOptions(result.TLSOpts)
	g.Expect(got.CipherSuites).To(Equal([]uint16{cryptotls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA, cryptotls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}))
}
