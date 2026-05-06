// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/inceptionstack/telemetron/internal/updater"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode             string         `mapstructure:"mode" yaml:"mode"`
	Endpoint         string         `mapstructure:"endpoint" yaml:"endpoint"`
	TokenFile        string         `mapstructure:"token_file" yaml:"token_file"`
	LogLevel         string         `mapstructure:"log_level" yaml:"log_level"`
	InsecureEndpoint bool           `mapstructure:"insecure_endpoint" yaml:"insecure_endpoint,omitempty"`
	RunAs            string         `mapstructure:"run_as" yaml:"run_as,omitempty"`
	Declared         DeclaredConfig `mapstructure:"declared" yaml:"declared,omitempty"`
	Collectors       map[string]any  `mapstructure:",remain" yaml:",inline,omitempty"`
	AutoUpdate       updater.Config  `mapstructure:"auto_update" yaml:"auto_update,omitempty"`

	FilePath string `mapstructure:"-" yaml:"-"`
	Paths    Paths  `mapstructure:"-" yaml:"-"`
}

type DeclaredConfig struct {
	DeploymentID string `mapstructure:"deployment_id" yaml:"deployment_id,omitempty"`
	Tier         string `mapstructure:"tier" yaml:"tier,omitempty"`
	Environment  string `mapstructure:"environment" yaml:"environment,omitempty"`
	PackVersion  string `mapstructure:"pack_version" yaml:"pack_version,omitempty"`
}

type Paths struct {
	ConfigPath    string
	TokenFile     string
	InstallIDFile string
	StateDir      string
	StatusFile    string
	Instance      string // empty = primary
}

type LoadOptions struct {
	ConfigPath    string
	Instance      string
	Overrides     map[string]any
	BootstrapOnly bool
	Platform      string
}

func Load(opts LoadOptions) (Config, error) {
	platform := opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	instance := opts.Instance
	if err := ValidateInstance(instance); err != nil {
		return Config{}, err
	}

	cfgPath := opts.ConfigPath
	if envPath := os.Getenv("TELEMETRON_CONFIG"); envPath != "" {
		cfgPath = envPath
	}

	// Derive instance from config path if not explicitly provided.
	// Config files named "config-<instance>.yaml" imply an instance.
	if instance == "" && cfgPath != "" {
		instance = instanceFromConfigPath(cfgPath)
		if err := ValidateInstance(instance); err != nil {
			return Config{}, err
		}
	}

	paths := InstancePaths(platform, instance)

	if cfgPath == "" {
		cfgPath = paths.ConfigPath
	}

	v := viper.New()
	v.SetConfigFile(cfgPath)
	v.SetConfigType("yaml")
	v.SetDefault("token_file", paths.TokenFile)
	v.SetDefault("log_level", "info")

	if _, err := os.Stat(cfgPath); err == nil {
		if err := v.ReadInConfig(); err != nil {
			return Config{}, err
		}
	}

	applyEnv(v)
	for key, value := range opts.Overrides {
		if value == "" {
			continue
		}
		v.Set(key, value)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, err
	}
	cfg.FilePath = cfgPath
	cfg.Paths = paths
	if cfg.Mode == "" {
		cfg.Mode = defaultMode()
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.Collectors == nil {
		cfg.Collectors = map[string]any{}
	}
	if err := cfg.ValidateBootstrap(); err != nil {
		return Config{}, err
	}
	if !opts.BootstrapOnly {
		raw, err := ResolveCollectorRaw(cfg.Mode, cfg.Paths, cfg.Collectors[cfg.Mode])
		if err != nil {
			return Config{}, err
		}
		cfg.Collectors[cfg.Mode] = raw
	}
	return cfg, nil
}

func applyEnv(v *viper.Viper) {
	envMap := map[string]any{
		"endpoint":   os.Getenv("TELEMETRON_ENDPOINT"),
		"token_file": os.Getenv("TELEMETRON_TOKEN_FILE"),
		"mode":       os.Getenv("TELEMETRON_MODE"),
		"log_level":  os.Getenv("TELEMETRON_LOG_LEVEL"),
	}
	for key, value := range envMap {
		if value != "" {
			v.Set(key, value)
		}
	}
}

func (c Config) ValidateBootstrap() error {
	if c.Endpoint == "" {
		return errors.New("endpoint is required")
	}
	if !strings.HasPrefix(strings.ToLower(c.Endpoint), "https://") && !c.InsecureEndpoint {
		return errors.New("endpoint must start with https:// unless insecure_endpoint is true")
	}
	if c.Mode == "" {
		return errors.New("mode is required")
	}
	if !modeRegistered(c.Mode) {
		return fmt.Errorf("unsupported mode %q", c.Mode)
	}
	if c.TokenFile == "" && os.Getenv("TELEMETRON_TOKEN") == "" {
		return errors.New("token_file or TELEMETRON_TOKEN is required")
	}
	return nil
}

func (c Config) Token() (string, error) {
	if token := os.Getenv("TELEMETRON_TOKEN"); token != "" {
		return strings.TrimSpace(token), nil
	}
	data, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (c Config) Marshal() ([]byte, error) {
	return yaml.Marshal(c)
}

func DefaultPaths(platform string) Paths {
	return InstancePaths(platform, "")
}

// validInstanceRe restricts instance names to safe path components.
var validInstanceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidateInstance checks that an instance name is safe for use in file paths.
// Returns an error if the name contains path traversal characters or is otherwise invalid.
func ValidateInstance(instance string) error {
	if instance == "" {
		return nil // primary
	}
	if len(instance) > 64 {
		return fmt.Errorf("instance name too long (max 64): %q", instance)
	}
	if !validInstanceRe.MatchString(instance) {
		return fmt.Errorf("invalid instance name %q: must match [a-z0-9][a-z0-9-]*", instance)
	}
	return nil
}

// InstancePaths returns paths for a named instance. Empty instance = primary.
func InstancePaths(platform, instance string) Paths {
	switch platform {
	case "darwin":
		home := userHomeDir()
		configDir := home + "/.config/telemetron"
		stateDir := home + "/.local/share/telemetron"
		if instance != "" {
			return Paths{
				ConfigPath:    configDir + "/config-" + instance + ".yaml",
				TokenFile:     configDir + "/token-" + instance,
				InstallIDFile: configDir + "/install-id-" + instance,
				StateDir:      stateDir,
				StatusFile:    stateDir + "/status-" + instance + ".json",
				Instance:      instance,
			}
		}
		return Paths{
			ConfigPath:    configDir + "/config.yaml",
			TokenFile:     configDir + "/token",
			InstallIDFile: configDir + "/install-id",
			StateDir:      stateDir,
			StatusFile:    stateDir + "/status.json",
		}
	default:
		if instance != "" {
			return Paths{
				ConfigPath:    "/etc/telemetron/config-" + instance + ".yaml",
				TokenFile:     "/etc/telemetron/token-" + instance,
				InstallIDFile: "/etc/telemetron/install-id-" + instance,
				StateDir:      "/var/lib/telemetron",
				StatusFile:    "/var/lib/telemetron/status-" + instance + ".json",
				Instance:      instance,
			}
		}
		return Paths{
			ConfigPath:    "/etc/telemetron/config.yaml",
			TokenFile:     "/etc/telemetron/token",
			InstallIDFile: "/etc/telemetron/install-id",
			StateDir:      "/var/lib/telemetron",
			StatusFile:    "/var/lib/telemetron/status.json",
		}
	}
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if current, err := user.Current(); err == nil && current.HomeDir != "" {
		return current.HomeDir
	}
	return "/tmp"
}

// instanceFromConfigPath extracts instance name from "config-<instance>.yaml" filenames.
// Returns empty string for primary configs ("config.yaml").
func instanceFromConfigPath(path string) string {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "config-") && strings.HasSuffix(base, ".yaml") {
		return strings.TrimSuffix(strings.TrimPrefix(base, "config-"), ".yaml")
	}
	return ""
}

type modeRegistration struct {
	decodeFn      func(raw any) (any, error)
	defaultConfig func(paths Paths) any
}

var (
	modeRegistryMu sync.RWMutex
	modeRegistry   = map[string]modeRegistration{}
)

func RegisterMode(name string, decodeFn func(raw any) (any, error), defaultConfig func(paths Paths) any) {
	modeRegistryMu.Lock()
	defer modeRegistryMu.Unlock()

	modeRegistry[name] = modeRegistration{
		decodeFn:      decodeFn,
		defaultConfig: defaultConfig,
	}
}

func ResolveCollectorRaw(name string, paths Paths, raw any) (any, error) {
	modeRegistryMu.RLock()
	registration, ok := modeRegistry[name]
	modeRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported mode %q", name)
	}

	merged := mergeMaps(toMap(registration.defaultConfig(paths)), toMap(raw))
	if registration.decodeFn != nil {
		if _, err := registration.decodeFn(merged); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

func RegisteredModes() []string {
	modeRegistryMu.RLock()
	defer modeRegistryMu.RUnlock()

	names := make([]string, 0, len(modeRegistry))
	for name := range modeRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func defaultMode() string {
	modes := RegisteredModes()
	if len(modes) == 0 {
		return ""
	}
	return modes[0]
}

func modeRegistered(name string) bool {
	modeRegistryMu.RLock()
	defer modeRegistryMu.RUnlock()

	_, ok := modeRegistry[name]
	return ok
}

func toMap(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return cloneMap(typed)
	}

	data, err := yaml.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	return cloneMap(out)
}

func mergeMaps(base, overlay map[string]any) map[string]any {
	merged := cloneMap(base)
	for key, value := range overlay {
		if baseMap, ok := merged[key].(map[string]any); ok {
			if overlayMap, ok := value.(map[string]any); ok {
				merged[key] = mergeMaps(baseMap, overlayMap)
				continue
			}
		}
		merged[key] = value
	}
	return merged
}

func cloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		if nested, ok := item.(map[string]any); ok {
			out[key] = cloneMap(nested)
			continue
		}
		out[key] = item
	}
	return out
}
