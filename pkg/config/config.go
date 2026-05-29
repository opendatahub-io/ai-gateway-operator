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

package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/spf13/viper"

	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	ofVersion "github.com/operator-framework/api/pkg/lib/version"
)

const (
	KeyMetricsAddr      = "metrics-bind-address"
	KeyHealthProbeAddr  = "health-probe-bind-address"
	KeyPprofAddr        = "pprof-addr"
	KeyLeaderElect      = "leader-elect"
	KeyLeaderElectionID = "leader-election-id"
	KeyManifestsPath    = "manifests-path"
	KeyApplicationsNS   = "applications-namespace"
	KeyPlatformType     = "platform-type"
	KeyPlatformVersion  = "platform-version"

	DefaultMetricsAddr      = ":8080"
	DefaultHealthProbeAddr  = ":8081"
	DefaultLeaderElect      = true
	DefaultLeaderElectionID = "opendatahub-ai-gateway-lock"
	DefaultApplicationsNS   = "opendatahub"
	DefaultPlatformType     = "unknown"
	DefaultPlatformVersion  = "unknown"

	// ConfigPathEnvVar is the environment variable that points to the mounted
	// ConfigMap directory (or a single config file).
	ConfigPathEnvVar = "ODH_MODULE_OPERATOR_CONFIGURATION_PATH"

	// EnvPrefix is the prefix for environment variables that override
	// configuration values (e.g. ODH_MODULE_OPERATOR_PLATFORM_TYPE).
	EnvPrefix = "ODH_MODULE_OPERATOR"
)

// structuredExtensions is the set of file extensions that are parsed as
// structured config (YAML, JSON) rather than simple key-value pairs.
var structuredExtensions = map[string]bool{
	"yaml": true,
	"yml":  true,
	"json": true,
}

// Config holds the complete operator configuration.
//
// Values are loaded from (in order of precedence):
//  1. Struct field defaults
//  2. ConfigMap files (from ODH_MODULE_OPERATOR_CONFIGURATION_PATH)
//  3. Environment variables (ODH_MODULE_OPERATOR_ prefix)
type Config struct {
	// MetricsAddr is the address the metrics endpoint binds to (0 to disable).
	MetricsAddr string `mapstructure:"metrics-bind-address"`
	// HealthProbeAddr is the address the health probe endpoint binds to.
	HealthProbeAddr string `mapstructure:"health-probe-bind-address"`
	// PprofAddr is the address the pprof endpoint binds to (empty = disabled).
	PprofAddr string `mapstructure:"pprof-addr"`
	// LeaderElect enables leader election for high availability.
	LeaderElect bool `mapstructure:"leader-elect"`
	// LeaderElectionID is the name of the leader election lock resource.
	LeaderElectionID string `mapstructure:"leader-election-id"`
	// ManifestsPath is the base path for component manifests.
	ManifestsPath string `mapstructure:"manifests-path"`
	// ApplicationsNamespace is the namespace where module workloads are deployed.
	ApplicationsNamespace string `mapstructure:"applications-namespace"`

	// PlatformType is the platform identifier (e.g. "OpenDataHub", "SelfManagedRHOAI").
	PlatformType string `mapstructure:"platform-type"`
	// PlatformVersion is the platform operator version.
	PlatformVersion string `mapstructure:"platform-version"`
}

// Release builds a common.Release from the configured platform type and
// version. If PlatformVersion is not valid semver, the version defaults
// to 0.0.0.
func (c *Config) Release() common.Release {
	rel := common.Release{
		Name: common.Platform(c.PlatformType),
	}

	if c.PlatformVersion != "" {
		v, err := semver.ParseTolerant(c.PlatformVersion)
		if err == nil {
			rel.Version = ofVersion.OperatorVersion{Version: v}
		}
	}

	return rel
}

// Load reads operator configuration from all available sources.
//
// The loading sequence:
//  1. Set defaults
//  2. Read ConfigMap files from ODH_MODULE_OPERATOR_CONFIGURATION_PATH (if set)
//  3. Bind environment variables with the ODH_MODULE_OPERATOR_ prefix
//  4. Unmarshal into the Config struct
func Load() (*Config, error) {
	var configFS fs.FS

	if configPath := os.Getenv(ConfigPathEnvVar); configPath != "" {
		configFS = os.DirFS(configPath)
	}

	return LoadFromFS(configFS)
}

// LoadFromFS reads operator configuration from the given filesystem.
// If fsys is nil, only defaults and environment variables are used.
// This function is the primary entry point for testing.
func LoadFromFS(fsys fs.FS) (*Config, error) {
	v := viper.New()

	setDefaults(v)

	if fsys != nil {
		if err := loadFromFS(v, fsys); err != nil {
			return nil, fmt.Errorf("loading config from filesystem: %w", err)
		}
	}

	if err := bindEnv(v); err != nil {
		return nil, fmt.Errorf("binding env vars: %w", err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault(KeyMetricsAddr, DefaultMetricsAddr)
	v.SetDefault(KeyHealthProbeAddr, DefaultHealthProbeAddr)
	v.SetDefault(KeyLeaderElect, DefaultLeaderElect)
	v.SetDefault(KeyLeaderElectionID, DefaultLeaderElectionID)
	v.SetDefault(KeyApplicationsNS, DefaultApplicationsNS)
	v.SetDefault(KeyManifestsPath, "")
	v.SetDefault(KeyPlatformType, DefaultPlatformType)
	v.SetDefault(KeyPlatformVersion, DefaultPlatformVersion)
}

func bindEnv(v *viper.Viper) error {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	// Explicit BindEnv so Unmarshal picks up env vars.
	// AutomaticEnv only works with Get(), not Unmarshal().
	for _, key := range v.AllKeys() {
		if err := v.BindEnv(key); err != nil {
			return fmt.Errorf("binding env for key %s: %w", key, err)
		}
	}

	return nil
}

// loadFromFS reads all files from the given fs.FS and sets them as
// viper key-value pairs. Structured files (YAML/JSON) are parsed and
// their keys merged. Plain files are treated as simple key-value pairs
// where the filename is the key and the content is the value.
func loadFromFS(v *viper.Viper, fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("reading config directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		data, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			continue
		}

		ext := strings.TrimPrefix(filepath.Ext(entry.Name()), ".")

		if structuredExtensions[ext] {
			if err := mergeStructuredFile(v, entry.Name(), ext, data); err != nil {
				return err
			}
		} else if err := mergeKeyValue(v, entry.Name(), data); err != nil {
			return err
		}
	}

	return nil
}

// mergeStructuredFile parses a YAML/JSON file and merges its keys into viper.
func mergeStructuredFile(v *viper.Viper, name string, ext string, data []byte) error {
	fv := viper.New()
	fv.SetConfigType(ext)

	if err := fv.ReadConfig(strings.NewReader(string(data))); err != nil {
		return fmt.Errorf("parsing config file %s: %w", name, err)
	}

	if err := v.MergeConfigMap(fv.AllSettings()); err != nil {
		return fmt.Errorf("merging config from %s: %w", name, err)
	}

	return nil
}

// mergeKeyValue treats a file as a simple key-value pair: the filename is
// the key and the trimmed content is the value. Uses MergeConfigMap so
// environment variables take precedence over file values.
func mergeKeyValue(v *viper.Viper, name string, data []byte) error {
	if err := v.MergeConfigMap(map[string]any{
		name: strings.TrimSpace(string(data)),
	}); err != nil {
		return fmt.Errorf("merging key %s: %w", name, err)
	}

	return nil
}
