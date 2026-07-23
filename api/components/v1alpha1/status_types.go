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

package v1alpha1

// SourceRenderer identifies how a manifest source is rendered.
// +kubebuilder:validation:Enum=kustomize;helm;template
type SourceRenderer string

const (
	SourceRendererKustomize SourceRenderer = "kustomize"
	SourceRendererHelm      SourceRenderer = "helm"
	SourceRendererTemplate  SourceRenderer = "template"
)

// SourceStatus describes a manifest source loaded during reconciliation.
// +kubebuilder:object:generate=true
type SourceStatus struct {
	// Path is the resolved manifest source path.
	Path string `json:"path"`
	// Renderer is the rendering engine used for this source.
	Renderer SourceRenderer `json:"renderer"`
}

// ModuleStatus reports the module operator's runtime information.
// The platform version is reported separately via status.releases.
// +kubebuilder:object:generate=true
type ModuleStatus struct {
	// Version is the module operator version.
	Version SemVer `json:"version"`
	// BuildSource identifies the source the operator was built from
	// in the format repo@branch/commit (e.g. github.com/org/repo@main/abc1234).
	BuildSource string `json:"buildSource,omitempty"`
	// Platform is the detected platform identifier (e.g. OpenShift AI Self-Managed).
	// The platform version is reported separately via status.releases.
	Platform string `json:"platform,omitempty"`
	// Sources lists the manifest sources loaded during reconciliation.
	Sources []SourceStatus `json:"sources,omitempty"`
}
