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
	DefaultWorkerMode   string  `mapstructure:"default_worker_mode"`
	ApprovalPersists    bool    `mapstructure:"approval_persists"`
	DashboardPort       int     `mapstructure:"dashboard_port"`
	DashboardHost       string  `mapstructure:"dashboard_host"`
	WatchDebounceMS     int     `mapstructure:"watch_debounce_ms"`
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
	v.SetDefault("default_worker_mode", "plan")
	v.SetDefault("approval_persists", false)
	v.SetDefault("dashboard_port", 7777)
	v.SetDefault("dashboard_host", "127.0.0.1")
	v.SetDefault("watch_debounce_ms", 100)

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
