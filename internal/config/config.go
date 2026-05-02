package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	DefaultConfigPath = "/etc/lokiotel/config.yaml"
	DefaultTokenFile  = "/etc/lokiotel/token"
	DefaultStateDir   = "/var/lib/lokiotel"
	DefaultStatusFile = "/var/lib/lokiotel/status.json"
)

type Config struct {
	Mode      string         `mapstructure:"mode"`
	Endpoint  string         `mapstructure:"endpoint"`
	TokenFile string         `mapstructure:"token_file"`
	LogLevel  string         `mapstructure:"log_level"`
	OpenClaw  OpenClawConfig `mapstructure:"openclaw"`
	Declared  DeclaredConfig `mapstructure:"declared"`
}

type OpenClawConfig struct {
	SessionDir    string        `mapstructure:"session_dir"`
	FlushInterval time.Duration `mapstructure:"flush_interval"`
	ScanInterval  time.Duration `mapstructure:"scan_interval"`
	StateFile     string        `mapstructure:"state_file"`
	StatusFile    string        `mapstructure:"status_file"`
}

type DeclaredConfig struct {
	DeploymentID string `mapstructure:"deployment_id"`
	Tier         string `mapstructure:"tier"`
	Environment  string `mapstructure:"environment"`
	PackVersion  string `mapstructure:"pack_version"`
}

type LoadOptions struct {
	ConfigPath string
	Overrides  map[string]string
}

func Load(opts LoadOptions) (Config, error) {
	cfgPath := opts.ConfigPath
	if envPath := os.Getenv("LOKIOTEL_CONFIG"); envPath != "" {
		cfgPath = envPath
	}
	if cfgPath == "" {
		cfgPath = DefaultConfigPath
	}

	v := viper.New()
	v.SetConfigFile(cfgPath)
	v.SetConfigType("yaml")
	v.SetDefault("token_file", DefaultTokenFile)
	v.SetDefault("log_level", "info")
	v.SetDefault("openclaw.flush_interval", "15s")
	v.SetDefault("openclaw.scan_interval", "15s")
	v.SetDefault("openclaw.state_file", filepath.Join(DefaultStateDir, "openclaw.state.json"))
	v.SetDefault("openclaw.status_file", DefaultStatusFile)

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

	if err := rejectUnknownModeKeys(v); err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, err
	}
	if cfg.Mode == "" {
		cfg.Mode = "openclaw"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyEnv(v *viper.Viper) {
	envMap := map[string]string{
		"endpoint":   os.Getenv("LOKIOTEL_ENDPOINT"),
		"token_file": os.Getenv("LOKIOTEL_TOKEN_FILE"),
		"mode":       os.Getenv("LOKIOTEL_MODE"),
		"log_level":  os.Getenv("LOKIOTEL_LOG_LEVEL"),
	}
	for key, value := range envMap {
		if value != "" {
			v.Set(key, value)
		}
	}
}

func rejectUnknownModeKeys(v *viper.Viper) error {
	settings := v.AllSettings()
	mode, _ := settings["mode"].(string)
	if mode == "" {
		mode = "openclaw"
	}
	if mode != "openclaw" {
		return nil
	}
	raw, ok := settings["openclaw"].(map[string]any)
	if !ok {
		return nil
	}
	allowed := map[string]struct{}{
		"session_dir":    {},
		"flush_interval": {},
		"scan_interval":  {},
		"state_file":     {},
		"status_file":    {},
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown openclaw config key %q", key)
		}
	}
	return nil
}

func (c Config) Validate() error {
	if c.Endpoint == "" {
		return errors.New("endpoint is required")
	}
	if c.Mode == "" {
		return errors.New("mode is required")
	}
	if c.TokenFile == "" && os.Getenv("LOKIOTEL_TOKEN") == "" {
		return errors.New("token_file or LOKIOTEL_TOKEN is required")
	}
	if c.Mode != "openclaw" {
		return fmt.Errorf("unsupported mode %q", c.Mode)
	}
	if strings.TrimSpace(c.OpenClaw.SessionDir) == "" {
		return errors.New("openclaw.session_dir is required")
	}
	if c.OpenClaw.FlushInterval <= 0 {
		return errors.New("openclaw.flush_interval must be > 0")
	}
	if c.OpenClaw.ScanInterval <= 0 {
		return errors.New("openclaw.scan_interval must be > 0")
	}
	if c.OpenClaw.StateFile == "" {
		return errors.New("openclaw.state_file is required")
	}
	if c.OpenClaw.StatusFile == "" {
		return errors.New("openclaw.status_file is required")
	}
	return nil
}

func (c Config) Token() (string, error) {
	if token := os.Getenv("LOKIOTEL_TOKEN"); token != "" {
		return strings.TrimSpace(token), nil
	}
	data, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (c Config) ToYAML() string {
	return fmt.Sprintf(`mode: %s
endpoint: %s
token_file: %s
log_level: %s
openclaw:
  session_dir: %s
  flush_interval: %s
  scan_interval: %s
  state_file: %s
  status_file: %s
declared:
  deployment_id: %s
  tier: %s
  environment: %s
  pack_version: %s
`,
		c.Mode,
		c.Endpoint,
		c.TokenFile,
		c.LogLevel,
		c.OpenClaw.SessionDir,
		c.OpenClaw.FlushInterval,
		c.OpenClaw.ScanInterval,
		c.OpenClaw.StateFile,
		c.OpenClaw.StatusFile,
		c.Declared.DeploymentID,
		c.Declared.Tier,
		c.Declared.Environment,
		c.Declared.PackVersion,
	)
}
