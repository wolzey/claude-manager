package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
	"github.com/wolzey/claude-manager/internal/store"
)

type Config struct {
	DefaultBudgetUSD    float64 `mapstructure:"default_budget_usd"`
	DefaultModel        string  `mapstructure:"default_model"`
	DefaultAllowedTools string  `mapstructure:"default_allowed_tools"`
	DefaultPermission   string  `mapstructure:"default_permission_mode"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(store.Root())
	v.SetEnvPrefix("CMGR")
	v.AutomaticEnv()

	v.SetDefault("default_budget_usd", 5.0)
	v.SetDefault("default_model", "")
	v.SetDefault("default_allowed_tools", "Read,Edit,Write,Bash(git status),Bash(git diff *),Bash(ls *),Bash(rg *),Bash(cat *),Grep,Glob")
	v.SetDefault("default_permission_mode", "acceptEdits")

	_ = os.MkdirAll(store.Root(), 0o755)
	cfgPath := filepath.Join(store.Root(), "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, err
	}
	return &c, nil
}
