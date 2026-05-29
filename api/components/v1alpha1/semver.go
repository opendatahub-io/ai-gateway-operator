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

	"github.com/blang/semver/v4"
)

// SemVer is a semantic version string with validated marshal/unmarshal.
// It serializes as a plain JSON string (e.g. "1.2.3").
type SemVer string

// NewSemVer creates a SemVer from a string, returning an error if it
// is not valid semver. The value is normalized through parse to ensure
// canonical form.
func NewSemVer(s string) (SemVer, error) {
	parsed, err := semver.ParseTolerant(s)
	if err != nil {
		return "", fmt.Errorf("invalid semver %q: %w", s, err)
	}

	return SemVer(parsed.String()), nil
}

func (v SemVer) String() string {
	return string(v)
}

// IsZero returns true if the version is empty (unset).
func (v SemVer) IsZero() bool {
	return v == ""
}

// Parse returns the parsed semver.Version.
func (v SemVer) Parse() (semver.Version, error) {
	return semver.ParseTolerant(string(v))
}

// GT returns true if v is strictly greater than other.
// Returns false if either version is unparseable.
func (v SemVer) GT(other SemVer) bool {
	cv, err := v.Parse()
	if err != nil {
		return false
	}

	ov, err := other.Parse()
	if err != nil {
		return false
	}

	return cv.GT(ov)
}

func (v SemVer) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(v))
}

func (v *SemVer) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	if s == "" {
		*v = ""
		return nil
	}

	parsed, err := semver.ParseTolerant(s)
	if err != nil {
		return fmt.Errorf("invalid semver %q: %w", s, err)
	}

	*v = SemVer(parsed.String())

	return nil
}
