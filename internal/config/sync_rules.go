package config

import (
	"errors"
)

type SyncRulesConfig struct {
	Template string              `yaml:"template" json:"template"`
	Custom   map[string]SyncRule `yaml:"custom" json:"custom"`
}

type SyncRule struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Fields  []string `yaml:"fields" json:"fields"`
}

func DefaultSyncRules() SyncRulesConfig {
	return SyncRulesConfig{
		Template: "full",
		Custom: map[string]SyncRule{
			"full":     {Enabled: true, Fields: []string{"status", "progress", "repeats", "review", "user_rating", "started_at", "finished_at"}},
			"progress": {Enabled: true, Fields: []string{"status", "progress"}},
			"ratings":  {Enabled: true, Fields: []string{"review", "user_rating"}},
		},
	}
}

func (r SyncRulesConfig) Validate() error {
	if r.Template == "" {
		return errors.New("sync_rules.template is required")
	}
	if len(r.Custom) == 0 {
		return errors.New("sync_rules.custom must include at least one rule")
	}
	return nil
}
