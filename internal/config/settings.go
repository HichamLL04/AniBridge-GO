package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"gopkg.in/yaml.v3"
)

type ScanMode string

const (
	ScanPeriodic ScanMode = "periodic"
	ScanPoll     ScanMode = "poll"
	ScanWebhook  ScanMode = "webhook"
)

type Config struct {
	Web             WebConfig       `yaml:"web" json:"web"`
	LogLevel        string          `yaml:"log_level" json:"log_level" env:"ANIBRIDGE_GO_LOG_LEVEL"`
	DataDir         string          `yaml:"data_dir" json:"data_dir" env:"ANIBRIDGE_GO_DATA_DIR"`
	MappingsURL     string          `yaml:"mappings_url" json:"mappings_url" env:"ANIBRIDGE_GO_MAPPINGS_URL"`
	ScanMode        ScanMode        `yaml:"scan_mode" json:"scan_mode" env:"ANIBRIDGE_GO_SCAN_MODE"`
	ScanInterval    Duration        `yaml:"scan_interval" json:"scan_interval"`
	PollInterval    Duration        `yaml:"poll_interval" json:"poll_interval"`
	Profiles        []ProfileConfig `yaml:"profiles" json:"profiles"`
	ProviderClasses []ProviderClass `yaml:"provider_classes" json:"provider_classes"`
	DryRun          bool            `yaml:"dry_run" json:"dry_run" env:"ANIBRIDGE_GO_DRY_RUN"`
	Backup          BackupConfig    `yaml:"backup" json:"backup"`
	SyncRules       SyncRulesConfig `yaml:"sync_rules" json:"sync_rules"`
}

type WebConfig struct {
	Host     string `yaml:"host" json:"host" env:"ANIBRIDGE_GO_WEB_HOST"`
	Port     int    `yaml:"port" json:"port" env:"ANIBRIDGE_GO_WEB_PORT"`
	Username string `yaml:"username" json:"username" env:"ANIBRIDGE_GO_WEB_USERNAME"`
	Password string `yaml:"password" json:"password" env:"ANIBRIDGE_GO_WEB_PASSWORD"`
	Htpasswd string `yaml:"htpasswd" json:"htpasswd" env:"ANIBRIDGE_GO_WEB_HTPASSWD"`
}

func (w WebConfig) Addr() string { return fmt.Sprintf("%s:%d", w.Host, w.Port) }

type ProviderClass struct {
	Namespace string         `yaml:"namespace" json:"namespace"`
	Type      string         `yaml:"type" json:"type"`
	Settings  map[string]any `yaml:"settings" json:"settings"`
}

type ProfileConfig struct {
	Name            string   `yaml:"name" json:"name"`
	LibraryProvider string   `yaml:"library_provider" json:"library_provider"`
	ListProvider    string   `yaml:"list_provider" json:"list_provider"`
	Fields          []string `yaml:"fields" json:"fields"`
	DryRun          *bool    `yaml:"dry_run,omitempty" json:"dry_run,omitempty"`
}

type BackupConfig struct {
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Dir      string   `yaml:"dir" json:"dir"`
	Keep     int      `yaml:"keep" json:"keep"`
	Schedule string   `yaml:"schedule" json:"schedule"`
	Include  []string `yaml:"include" json:"include"`
}

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

type envOverrides struct {
	LogLevel    string `env:"ANIBRIDGE_GO_LOG_LEVEL"`
	DataDir     string `env:"ANIBRIDGE_GO_DATA_DIR"`
	MappingsURL string `env:"ANIBRIDGE_GO_MAPPINGS_URL"`
	ScanMode    string `env:"ANIBRIDGE_GO_SCAN_MODE"`
	DryRun      *bool  `env:"ANIBRIDGE_GO_DRY_RUN"`
	WebHost     string `env:"ANIBRIDGE_GO_WEB_HOST"`
	WebPort     int    `env:"ANIBRIDGE_GO_WEB_PORT"`
	WebUser     string `env:"ANIBRIDGE_GO_WEB_USERNAME"`
	WebPass     string `env:"ANIBRIDGE_GO_WEB_PASSWORD"`
	Htpasswd    string `env:"ANIBRIDGE_GO_WEB_HTPASSWD"`
}

func Default() Config {
	return Config{
		Web:          WebConfig{Host: "0.0.0.0", Port: 8080},
		LogLevel:     "info",
		DataDir:      "./data",
		MappingsURL:  "https://github.com/anibridge/anibridge-mappings/releases/download/v3/mappings.json.zst",
		ScanMode:     ScanPeriodic,
		ScanInterval: Duration{12 * time.Hour},
		PollInterval: Duration{5 * time.Minute},
		Profiles:     []ProfileConfig{},
		ProviderClasses: []ProviderClass{
			{Namespace: "anilist", Type: "list", Settings: map[string]any{}},
		},
		Backup:    BackupConfig{Enabled: true, Dir: "backups", Keep: 7, Schedule: "0 3 * * *", Include: []string{"config", "database"}},
		SyncRules: DefaultSyncRules(),
	}
}

func EffectivePath(path string) string {
	if path != "" {
		return path
	}
	if p := os.Getenv("ANIBRIDGE_GO_CONFIG"); p != "" {
		return p
	}
	return "config.yml"
}

func Load(path string) (Config, error) {
	cfg := Default()
	path = EffectivePath(path)
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return cfg, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, err
	}

	var ov envOverrides
	if err := env.Parse(&ov); err != nil {
		return cfg, err
	}
	applyEnv(&cfg, ov)
	return cfg, cfg.Validate()
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func applyEnv(cfg *Config, ov envOverrides) {
	if ov.LogLevel != "" {
		cfg.LogLevel = ov.LogLevel
	}
	if ov.DataDir != "" {
		cfg.DataDir = ov.DataDir
	}
	if ov.MappingsURL != "" {
		cfg.MappingsURL = ov.MappingsURL
	}
	if ov.ScanMode != "" {
		cfg.ScanMode = ScanMode(ov.ScanMode)
	}
	if ov.DryRun != nil {
		cfg.DryRun = *ov.DryRun
	}
	if ov.WebHost != "" {
		cfg.Web.Host = ov.WebHost
	}
	if ov.WebPort != 0 {
		cfg.Web.Port = ov.WebPort
	}
	if ov.WebUser != "" {
		cfg.Web.Username = ov.WebUser
	}
	if ov.WebPass != "" {
		cfg.Web.Password = ov.WebPass
	}
	if ov.Htpasswd != "" {
		cfg.Web.Htpasswd = ov.Htpasswd
	}
}

func (c Config) Validate() error {
	level := strings.ToLower(c.LogLevel)
	if level != "debug" && level != "info" && level != "success" && level != "warn" && level != "error" {
		return fmt.Errorf("invalid log_level %q", c.LogLevel)
	}
	if c.DataDir == "" {
		return errors.New("data_dir is required")
	}
	if c.Web.Port < 1 || c.Web.Port > 65535 {
		return fmt.Errorf("web.port must be between 1 and 65535")
	}
	switch c.ScanMode {
	case ScanPeriodic:
		if c.ScanInterval.Duration <= 0 {
			return errors.New("scan_interval must be positive")
		}
	case ScanPoll:
		if c.PollInterval.Duration <= 0 {
			return errors.New("poll_interval must be positive")
		}
	case ScanWebhook:
	default:
		return fmt.Errorf("invalid scan_mode %q", c.ScanMode)
	}
	seen := map[string]bool{}
	for _, p := range c.ProviderClasses {
		if p.Namespace == "" || p.Type == "" {
			return errors.New("provider_classes entries require namespace and type")
		}
		seen[p.Namespace] = true
	}
	for _, p := range c.Profiles {
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("profile name is required")
		}
		if !seen[p.LibraryProvider] && p.LibraryProvider != "" {
			return fmt.Errorf("profile %q references unknown library provider %q", p.Name, p.LibraryProvider)
		}
		if !seen[p.ListProvider] && p.ListProvider != "" {
			return fmt.Errorf("profile %q references unknown list provider %q", p.Name, p.ListProvider)
		}
	}
	return c.SyncRules.Validate()
}

func JSONSchema() map[string]any {
	return map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"title":    "AniBridge GO configuration",
		"type":     "object",
		"required": []string{"web", "log_level", "data_dir", "scan_mode", "profiles"},
		"properties": map[string]any{
			"log_level":     map[string]any{"type": "string", "enum": []string{"debug", "info", "success", "warn", "error"}},
			"data_dir":      map[string]any{"type": "string"},
			"mappings_url":  map[string]any{"type": "string"},
			"scan_mode":     map[string]any{"type": "string", "enum": []string{"periodic", "poll", "webhook"}},
			"scan_interval": map[string]any{"type": "string", "default": "12h"},
			"poll_interval": map[string]any{"type": "string", "default": "5m"},
			"dry_run":       map[string]any{"type": "boolean"},
		},
	}
}
