package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PollInterval              time.Duration    `yaml:"-"`
	PollIntervalRaw           string           `yaml:"poll_interval"`
	SlippageRate              float64          `yaml:"slippage_rate"`
	FeeRate                   float64          `yaml:"fee_rate"`
	PriceHistoryRetentionDays int              `yaml:"price_history_retention_days"`
	WarmupBars                int              `yaml:"warmup_bars"`
	Strategies                []StrategyConfig `yaml:"strategies"`
}

type StrategyConfig struct {
	Name        string                 `yaml:"name"`
	Type        string                 `yaml:"type"`
	InitialCash float64                `yaml:"initial_cash"`
	Params      map[string]interface{} `yaml:"params"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.PollIntervalRaw != "" {
		d, err := time.ParseDuration(cfg.PollIntervalRaw)
		if err != nil {
			return nil, fmt.Errorf("parse poll_interval: %w", err)
		}
		cfg.PollInterval = d
	} else {
		cfg.PollInterval = time.Second
	}
	if cfg.SlippageRate == 0 {
		cfg.SlippageRate = 0.0005
	}
	if cfg.FeeRate == 0 {
		cfg.FeeRate = 0.001
	}
	if cfg.PriceHistoryRetentionDays == 0 {
		cfg.PriceHistoryRetentionDays = 180
	}
	if cfg.WarmupBars == 0 {
		cfg.WarmupBars = 500
	}
	return &cfg, nil
}

func GetFloat(params map[string]interface{}, key string, def float64) float64 {
	v, ok := params[key]
	if !ok {
		return def
	}
	switch f := v.(type) {
	case float64:
		return f
	case int:
		return float64(f)
	}
	return def
}

func GetInt(params map[string]interface{}, key string, def int) int {
	v, ok := params[key]
	if !ok {
		return def
	}
	switch i := v.(type) {
	case int:
		return i
	case float64:
		return int(i)
	}
	return def
}
