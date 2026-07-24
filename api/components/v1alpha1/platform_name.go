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

import (
	"encoding/json"
	"fmt"
)

// PlatformName is the detected platform identifier (e.g. OpenShift AI Self-Managed).
//
// It serializes as a plain JSON string. For read compatibility with operators that
// previously wrote status.module.platform as {"name":"...","version":"..."},
// UnmarshalJSON (and therefore unstructured→typed conversion) also accepts that
// legacy object shape and keeps only the name.
//
// +kubebuilder:validation:Type=string
type PlatformName string

func (p PlatformName) String() string {
	return string(p)
}

func (p PlatformName) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(p))
}

func (p *PlatformName) UnmarshalJSON(data []byte) error {
	if p == nil {
		return fmt.Errorf("PlatformName: UnmarshalJSON on nil pointer")
	}

	// Canonical form: "OpenDataHub"
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*p = PlatformName(asString)
		return nil
	}

	// Legacy form written before platform was flattened to a string:
	// {"name":"OpenDataHub","version":"0.0.0"}
	var asObject struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &asObject); err != nil {
		return fmt.Errorf("PlatformName: expected string or object with name: %w", err)
	}

	*p = PlatformName(asObject.Name)
	return nil
}
