package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// StationConfig is the configuration for `radio station`.
type StationConfig struct {
	Server struct {
		GRPCAddr string `yaml:"grpc_addr"`
	} `yaml:"server"`

	Auth struct {
		JWT string `yaml:"jwt"`
	} `yaml:"auth"`

	Station struct {
		Slug string `yaml:"slug"`
	} `yaml:"station"`

	// API configures this station's own optional local control HTTP API.
	// Only a placeholder /status route is implemented in this phase; real
	// control endpoints arrive with the future queue-API redesign.
	API struct {
		Enabled  bool   `yaml:"enabled"`
		BindHost string `yaml:"bind_host"`
		APIKey   string `yaml:"api_key"`
	} `yaml:"api"`

	Logging struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
}

// LoadStationConfig reads and parses a StationConfig from path, then applies
// defaults for any fields left unset.
func LoadStationConfig(path string) (*StationConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg := &StationConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg.applyDefaults()

	if v := os.Getenv("GORADIO_JWT"); v != "" {
		cfg.Auth.JWT = v
	}

	return cfg, nil
}

func (c *StationConfig) applyDefaults() {
	if c.Server.GRPCAddr == "" {
		c.Server.GRPCAddr = "localhost:9090"
	}
	if c.API.BindHost == "" {
		c.API.BindHost = "127.0.0.1:8091"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
}
