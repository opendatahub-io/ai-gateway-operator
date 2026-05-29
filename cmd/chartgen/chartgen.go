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

package chartgen

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	gvk "github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
)

const (
	defaultOutputDir   = "config/chart"
	defaultChartName   = "opendatahub-ai-gateway-operator"
	defaultChartVer    = "0.1.0"
	templatesDirName   = "templates"
	chartYAMLFilename  = "Chart.yaml"
	helpersTplFilename = "_helpers.tpl"
	valuesYAMLFilename = "values.yaml"
	valuesSchemaFile   = "values.schema.json"
	coreAPIGroup       = "core"
)

// NewCommand returns the cobra command for the chartgen subcommand.
func NewCommand() *cobra.Command {
	var outputDir string
	var chartName string
	var chartVersion string

	cmd := &cobra.Command{
		Use:   "chartgen",
		Short: "Generate a Helm chart from kustomize YAML on stdin",
		Long: `Reads multi-document Kubernetes YAML from stdin (typically piped from
kustomize build) and generates a Helm chart with proper templating.

Example:
  kustomize build config/default | manager chartgen --output config/chart`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(os.Stdin, outputDir, chartName, chartVersion)
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", defaultOutputDir, "Output directory for the chart")
	cmd.Flags().StringVar(&chartName, "name", defaultChartName, "Chart name")
	cmd.Flags().StringVar(&chartVersion, "version", defaultChartVer, "Chart version")

	return cmd
}

func run(
	reader io.Reader,
	outputDir string,
	chartName string,
	chartVersion string,
) error {
	resources, err := decodeResources(reader)
	if err != nil {
		return fmt.Errorf("decoding resources: %w", err)
	}

	// Group resources by GVK, skip Namespaces
	groups := groupByGVK(resources)

	// Extract defaults from the Deployment resource
	values := ExtractDefaults(resources)

	// Create output directories
	templatesDir := filepath.Join(outputDir, templatesDirName)
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}

	// Write Chart.yaml (only if missing)
	chartFile := filepath.Join(outputDir, chartYAMLFilename)
	if _, err := os.Stat(chartFile); os.IsNotExist(err) {
		if err := writeChartYAML(chartFile, chartName, chartVersion); err != nil {
			return fmt.Errorf("writing %s: %w", chartYAMLFilename, err)
		}
	}

	// Write _helpers.tpl
	if err := writeHelpersTpl(filepath.Join(templatesDir, helpersTplFilename)); err != nil {
		return fmt.Errorf("writing %s: %w", helpersTplFilename, err)
	}

	// Write values.yaml
	if err := WriteValuesYAML(values, filepath.Join(outputDir, valuesYAMLFilename)); err != nil {
		return fmt.Errorf("writing %s: %w", valuesYAMLFilename, err)
	}

	// Write values.schema.json
	if err := WriteValuesSchema(filepath.Join(outputDir, valuesSchemaFile)); err != nil {
		return fmt.Errorf("writing %s: %w", valuesSchemaFile, err)
	}

	// Write grouped resource templates
	for resourceGVK, res := range groups {
		filename := gvkToFilename(resourceGVK)
		path := filepath.Join(templatesDir, filename)

		content, err := renderGroup(resourceGVK, res)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", filename, err)
		}

		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	fmt.Fprintf(os.Stderr, "Helm chart generated at %s\n", outputDir)

	return nil
}

// decodeResources reads multi-document YAML from a reader and returns
// a slice of unstructured resources.
func decodeResources(reader io.Reader) ([]unstructured.Unstructured, error) {
	var resources []unstructured.Unstructured

	yr := utilyaml.NewYAMLReader(bufio.NewReader(reader))

	for {
		data, err := yr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading YAML document: %w", err)
		}

		data = []byte(strings.TrimSpace(string(data)))
		if len(data) == 0 {
			continue
		}

		var obj unstructured.Unstructured
		if err := yaml.Unmarshal(data, &obj.Object); err != nil {
			return nil, fmt.Errorf("unmarshaling resource: %w", err)
		}

		if obj.Object == nil {
			continue
		}

		resources = append(resources, obj)
	}

	return resources, nil
}

// groupByGVK groups resources by their GroupVersionKind, skipping Namespaces.
func groupByGVK(resources []unstructured.Unstructured) map[schema.GroupVersionKind][]unstructured.Unstructured {
	groups := make(map[schema.GroupVersionKind][]unstructured.Unstructured)

	for _, r := range resources {
		resourceGVK := r.GroupVersionKind()

		// Skip Namespace resources -- Helm manages namespace via --namespace
		if resourceGVK == gvk.Namespace {
			continue
		}

		groups[resourceGVK] = append(groups[resourceGVK], r)
	}

	return groups
}

// gvkToFilename converts a GVK to a template filename.
// Uses the full unambiguous format: <group>_<version>_<kind>.yaml
// Core API group (empty string) is rendered as "core".
func gvkToFilename(resourceGVK schema.GroupVersionKind) string {
	group := strings.ToLower(resourceGVK.Group)
	if group == "" {
		group = coreAPIGroup
	}

	return fmt.Sprintf("%s_%s_%s.yaml",
		group,
		strings.ToLower(resourceGVK.Version),
		strings.ToLower(resourceGVK.Kind),
	)
}
