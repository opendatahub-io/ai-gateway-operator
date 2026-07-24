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
	"context"
	"crypto/tls"
	"errors"
	"fmt"
"time"

	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	componentsv1alpha1 "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-operator/internal/controller/aigateway"
	libcache "github.com/opendatahub-io/ai-gateway-operator/pkg/cache"
	moduleconfig "github.com/opendatahub-io/ai-gateway-operator/pkg/config"
	dsciv2 "github.com/opendatahub-io/opendatahub-operator/v2/api/dscinitialization/v2"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhmanager "github.com/opendatahub-io/opendatahub-operator/v2/pkg/manager"
)

const (
	healthCheckName = "healthz"
	readyCheckName  = "readyz"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(dsciv2.AddToScheme(scheme))
}

// NewCommand returns the cobra command for the operator subcommand.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Start the module operator",
		RunE:  run,
	}

	return cmd
}

func run(cmd *cobra.Command, _ []string) error {
	// Load operator config from ConfigMap files, env vars.
	cfg, err := moduleconfig.Load()
	if err != nil {
		return fmt.Errorf("loading operator config: %w", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))
	log := ctrl.Log.WithName("setup")

	// Set the applications namespace so that the operator's kustomize render
	// action can determine the target namespace without requiring DSCI.
	viper.Set("rhai-applications-namespace", cfg.ApplicationsNamespace)
	cluster.SetRHAIApplicationNamespace(cfg.ApplicationsNamespace)

	// Fetch cluster TLS profile to configure secure metrics serving.
	restCfg := ctrl.GetConfigOrDie()

	bootstrapClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("creating bootstrap client: %w", err)
	}

	bootstrapCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tlsOpts []func(*tls.Config)

	tlsProfileFetched := false
	profile, err := tlspkg.FetchAPIServerTLSProfile(bootstrapCtx, bootstrapClient)
	if err != nil {
		switch {
		case apimeta.IsNoMatchError(err):
			log.Info("TLS profile not available (non-OpenShift cluster)")
		case apierrors.IsNotFound(err):
			log.Info("APIServer resource not found, using defaults")
		case apierrors.IsServiceUnavailable(err),
			apierrors.IsTimeout(err),
			apierrors.IsServerTimeout(err),
			apierrors.IsTooManyRequests(err),
			errors.Is(err, context.DeadlineExceeded):
			log.Info("Transient API error, using Intermediate defaults", "error", err)
			tlsProfileFetched = true
		default:
			return fmt.Errorf("unable to read TLS profile: %w", err)
		}

		profile = *configv1.TLSProfiles[configv1.TLSProfileIntermediateType]
	} else {
		tlsProfileFetched = true
	}

	tlsConfigFn, unsupported := tlspkg.NewTLSConfigFromProfile(profile)
	if len(unsupported) > 0 {
		log.Info("TLS profile contains unsupported ciphers", "unsupported", unsupported)
	}

	tlsOpts = append(tlsOpts, tlsConfigFn)

	tlsAdherenceFetched := false
	tlsAdherence, adherenceErr := tlspkg.FetchAPIServerTLSAdherencePolicy(bootstrapCtx, bootstrapClient)
	if adherenceErr != nil {
		log.Info("unable to fetch TLS adherence policy, watcher will retry", "error", adherenceErr)
	} else {
		tlsAdherenceFetched = true
	}

	tlsOpts = append(tlsOpts, func(c *tls.Config) {
		c.NextProtos = []string{"h2", "http/1.1"}
	})

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   cfg.MetricsAddr,
			SecureServing: true,
			CertDir:       "/tmp/k8s-metrics-server/metrics-certs",
			TLSOpts:       tlsOpts,
		},
		HealthProbeBindAddress:        cfg.HealthProbeAddr,
		PprofBindAddress:              cfg.PprofAddr,
		LeaderElection:                cfg.LeaderElect,
		LeaderElectionID:              cfg.LeaderElectionID,
		LeaderElectionReleaseOnCancel: true,

		// Cache configuration:
		// - Strip managedFields and last-applied-configuration to reduce memory.
		// - Scope the default watch to the applications namespace and
		//   cluster-scoped resources.
		Cache: cache.Options{
			DefaultTransform: libcache.StripUnusedFields(),
			DefaultNamespaces: map[string]cache.Config{
				cfg.ApplicationsNamespace: {},
				cache.AllNamespaces:       {},
			},
			// TODO: re-enable once CRD informer issue is resolved
			// ReaderFailOnMissingInformer: true,
		},

		// Client configuration:
		// - Enable cache reads for unstructured objects (used by kustomize rendering).
		// - Disable caching for ConfigMaps and Secrets so they are always read
		//   fresh (they change frequently and may contain sensitive data).
		Client: client.Options{
			Cache: &client.CacheOptions{
				Unstructured: true,
				DisableFor: []client.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
			},
		},
	}

	ctrlMgr, err := ctrl.NewManager(restCfg, mgrOpts)
	if err != nil {
		return fmt.Errorf("creating manager: %w", err)
	}

	// Wrap the manager with the manifests base path provider so that
	// ReconcilerFor can read it via the manifestsBasePathProvider interface.
	mgr := odhmanager.New(
		ctrlMgr,
		odhmanager.WithManifestsBasePath(cfg.ManifestsPath),
	)

	// Build the release once -- it is constant for the process lifetime.
	// The reconciler framework's cluster.GetRelease() is not populated
	// because this standalone operator does not call cluster.Init().
	rel := cfg.Release()

	// Register controllers.
	if err := aigateway.NewReconciler(cmd.Context(), mgr, cfg, rel); err != nil {
		return fmt.Errorf("creating aigateway reconciler: %w", err)
	}

	if err := mgr.AddHealthzCheck(healthCheckName, healthz.Ping); err != nil {
		return fmt.Errorf("setting up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck(readyCheckName, healthz.Ping); err != nil {
		return fmt.Errorf("setting up ready check: %w", err)
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	if tlsProfileFetched {
		watcher := &tlspkg.SecurityProfileWatcher{
			Client:                mgr.GetClient(),
			InitialTLSProfileSpec: profile,
			OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
				log.Info("TLS profile changed, initiating shutdown to reload")
				cancel()
			},
		}
		if tlsAdherenceFetched {
			watcher.InitialTLSAdherencePolicy = tlsAdherence
			watcher.OnAdherencePolicyChange = func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
				log.Info("TLS adherence policy changed, initiating shutdown to reload")
				cancel()
			}
		}
		if err := watcher.SetupWithManager(mgr); err != nil {
			return fmt.Errorf("setting up TLS profile watcher: %w", err)
		}
	}

	return mgr.Start(ctx)
}
