// Guard for the maas-discovery withholding in 3.6.
//
// The maas-discovery service is not shippable in this release (feature not
// ready; picked up again next release). The vendored maas-controller manifests
// must therefore carry the discovery IMAGE reference (so the CSV-declared
// RELATED_IMAGE_ODH_MAAS_DISCOVERY_IMAGE resolves) but none of the knobs that
// could turn the service on: with MAAS_DISCOVERY_ENABLED absent from the
// maas-controller Deployment, editing the maas-parameters ConfigMap has no
// effect and the controller defaults DiscoveryEnabled to false.
//
// hack/scripts/get-manifests.sh re-applies this withholding on every vendoring
// bump (strip_maas_discovery_knobs). This test exists so that a bump which
// restores the knobs — e.g. because the upstream manifest shape changed and the
// strip silently no longer matches — fails CI instead of quietly making the
// feature enableable again. Remove this test together with the script special
// case when the feature ships.
//
// NOTE: this test deliberately lives OUTSIDE config/manifests/maascontroller —
// that directory is wiped and re-copied by make get-manifests.
package manifests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/yaml"
)

// maascontrollerDir is the vendored maas-controller manifest tree under test.
var maascontrollerDir = filepath.Join("..", "..", "config", "manifests", "maascontroller")

// withheldEnvVars must not appear on the maas-controller Deployment.
var withheldEnvVars = []string{
	"MAAS_DISCOVERY_ENABLED",
	"MAAS_DISCOVERY_REPLICAS",
	"MAAS_DISCOVERY_LOG_LEVEL",
}

// withheldParamKeys must not appear in any vendored params.env.
var withheldParamKeys = []string{
	"maas-discovery-enabled",
	"maas-discovery-replicas",
	"maas-discovery-log-level",
}

func TestManagerDeploymentWithholdsDiscoveryKnobs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(maascontrollerDir, "manager", "manager.yaml"))
	if err != nil {
		t.Fatalf("read manager.yaml: %v", err)
	}

	var dep appsv1.Deployment
	if err := yaml.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("unmarshal manager.yaml: %v", err)
	}

	var envNames []string
	for _, c := range dep.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			envNames = append(envNames, e.Name)
		}
	}

	for _, withheld := range withheldEnvVars {
		for _, name := range envNames {
			if name == withheld {
				t.Errorf("maas-controller Deployment must not set %s: "+
					"the maas-discovery service is not shippable in this release "+
					"(see strip_maas_discovery_knobs in hack/scripts/get-manifests.sh)", withheld)
			}
		}
	}

	// The image reference must survive the withholding, otherwise the
	// CSV-declared RELATED_IMAGE_ODH_MAAS_DISCOVERY_IMAGE points nowhere.
	for _, want := range []string{"RELATED_IMAGE_ODH_MAAS_DISCOVERY_IMAGE"} {
		found := false
		for _, name := range envNames {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("maas-controller Deployment is missing %s; the discovery image plumbing must be kept", want)
		}
	}
}

func TestParamsEnvWithholdsDiscoveryKnobs(t *testing.T) {
	var paramsFiles []string
	err := filepath.WalkDir(maascontrollerDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "params.env" {
			paramsFiles = append(paramsFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk params.env files: %v", err)
	}
	if len(paramsFiles) == 0 {
		t.Fatal("no params.env files found under config/manifests/maascontroller; the vendored layout changed — update this test")
	}

	for _, file := range paramsFiles {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		var keys []string
		for _, line := range strings.Split(string(raw), "\n") {
			if key, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(key, "#") {
				keys = append(keys, key)
			}
		}

		for _, withheld := range withheldParamKeys {
			for _, key := range keys {
				if key == withheld {
					t.Errorf("%s must not define %s: "+
						"the maas-discovery service is not shippable in this release "+
						"(see strip_maas_discovery_knobs in hack/scripts/get-manifests.sh)", file, withheld)
				}
			}
		}

		// Every overlay must keep the image param so the RELATED_IMAGE env var
		// resolves on product installs.
		found := false
		for _, key := range keys {
			if key == "maas-discovery-image" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is missing maas-discovery-image; the discovery image plumbing must be kept", file)
		}
	}
}
