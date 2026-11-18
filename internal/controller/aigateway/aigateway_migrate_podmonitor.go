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

package aigateway

import (
	"context"
	"errors"
	"reflect"
	"strings"

	k8serr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/ai-gateway-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	odhannotations "github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/annotations"
)

// maasControllerLegacyPodMonitorName is the PodMonitor the maas-controller bundle
// shipped up to RHOAI 3.5. A ServiceMonitor of the same name that scrapes over HTTPS
// replaced it.
const maasControllerLegacyPodMonitorName = "maas-controller-metrics"

// deleteLegacyMaasControllerPodMonitor deletes that PodMonitor. It has no owner, so an
// upgrade leaves it behind, scraping the now-HTTPS metrics port over plain HTTP:
// TargetDown fires and maas-controller logs a TLS handshake error every 30s. It runs on
// every reconcile rather than from upgrade(), which fires only when a recorded module
// version increases; once the PodMonitor is gone it costs one uncached read.
//
// The cleanup is best effort: a failure is logged and retried on the next reconcile
// instead of failing the reconcile, since the deploy does not depend on it.
func (m *Module) deleteLegacyMaasControllerPodMonitor(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.AIGateway)
	if !ok {
		return errors.New("instance is not an AIGateway")
	}
	// Otherwise a PodMonitor of that name belongs to whoever deploys maas-controller now.
	if obj.Spec.ModelsAsAService.ManagementState != managedState {
		return nil
	}
	if rr.Client == nil {
		return errors.New("reconciliation client is nil")
	}

	log := logf.FromContext(ctx)
	pm := &unstructured.Unstructured{}
	pm.SetGroupVersionKind(gvk.CoreosPodMonitor)
	key := client.ObjectKey{Namespace: m.cfg.ApplicationsNamespace, Name: maasControllerLegacyPodMonitorName}
	// Through the cache this one-off lookup would start a cluster-wide PodMonitor
	// informer that lives as long as the operator.
	if err := m.apiReader.Get(ctx, key, pm); err != nil {
		// NoMatch: without the PodMonitor CRD there is nothing to clean up.
		if !k8serr.IsNotFound(err) && !apimeta.IsNoMatchError(err) {
			log.Error(err, "reading the maas-controller PodMonitor left over from RHOAI 3.5; retrying on the next reconcile", "podMonitor", key)
		}
		return nil
	}
	if !isLegacyMaasControllerPodMonitor(pm) || pm.GetAnnotations()[odhannotations.ManagedByODHOperator] == "false" {
		return nil
	}

	log.Info("deleting the maas-controller PodMonitor left over from RHOAI 3.5", "podMonitor", key)
	// As in migrateMaasControllerSelector, never delete an object other than the one
	// just checked (CWE-367); a Conflict leaves it for the next reconcile.
	preconditions := &client.Preconditions{UID: new(pm.GetUID()), ResourceVersion: new(pm.GetResourceVersion())}
	if err := rr.Client.Delete(ctx, pm, preconditions); err != nil && !k8serr.IsNotFound(err) && !k8serr.IsConflict(err) {
		log.Error(err, "deleting the maas-controller PodMonitor left over from RHOAI 3.5; retrying on the next reconcile", "podMonitor", key)
	}
	return nil
}

// isLegacyMaasControllerPodMonitor reports whether pm has the shape the old bundle
// shipped: its pod selector and one plain-HTTP endpoint on port metrics, path
// /metrics, and no owner but an AIGateway, so a PodMonitor a user created or
// customized is kept. Labels and the scrape interval are not compared: the render
// rewrites labels, and the interval does not change what is scraped.
func isLegacyMaasControllerPodMonitor(pm *unstructured.Unstructured) bool {
	if owner := metav1.GetControllerOf(pm); owner != nil && owner.Kind != componentApi.AIGatewayKind {
		return false
	}
	spec, _, _ := unstructured.NestedMap(pm.Object, "spec")
	if len(spec) != 2 || !reflect.DeepEqual(spec["selector"], map[string]any{"matchLabels": map[string]any{"control-plane": "maas-controller"}}) {
		return false
	}
	endpoints, _ := spec["podMetricsEndpoints"].([]any)
	if len(endpoints) != 1 {
		return false
	}
	endpoint, _ := endpoints[0].(map[string]any)
	if endpoint["port"] != "metrics" {
		return false
	}
	for key, value := range endpoint {
		text, _ := value.(string)
		switch {
		case key == "port", key == "interval":
		case key == "path" && (text == "" || text == "/metrics"):
		case key == "scheme" && (text == "" || strings.EqualFold(text, "http")):
		default:
			return false
		}
	}
	return true
}
