package config

import (
	"errors"
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Proxy    ProxyConfig    `yaml:"proxy"`
	Backends []BackendEntry `yaml:"backends"`
}

type ProxyConfig struct {
	Listen           string `yaml:"listen"`
	APIListen        string `yaml:"api_listen"`
	StandbyQueueSize int    `yaml:"standby_queue_size"`
}

type BackendEntry struct {
	Name        string `yaml:"name"`
	Addr        string `yaml:"addr"`
	Role        string `yaml:"role"`
	PoolSize    int    `yaml:"pool_size"`
	MaxPoolSize int    `yaml:"max_pool_size"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Proxy.Listen == "" {
		cfg.Proxy.Listen = "0.0.0.0:6379"
	}
	if cfg.Proxy.APIListen == "" {
		cfg.Proxy.APIListen = "0.0.0.0:8080"
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Backends) == 0 {
		return errors.New("config: at least one backend is required")
	}

	names := make(map[string]bool)
	hasPrimary := false

	for i := range c.Backends {
		b := &c.Backends[i]

		if b.Name == "" {
			return fmt.Errorf("config: backend %d: name is required", i)
		}
		if b.Addr == "" {
			return fmt.Errorf("config: backend %q: addr is required", b.Name)
		}
		if _, _, err := net.SplitHostPort(b.Addr); err != nil {
			return fmt.Errorf("config: backend %q: invalid addr %q: %w", b.Name, b.Addr, err)
		}

		switch b.Role {
		case "primary", "standby":
		default:
			return fmt.Errorf("config: backend %q: role must be 'primary' or 'standby', got %q", b.Name, b.Role)
		}

		if names[b.Name] {
			return fmt.Errorf("config: duplicate backend name %q", b.Name)
		}
		names[b.Name] = true

		if b.Role == "primary" {
			hasPrimary = true
		}
	}

	if !hasPrimary {
		return errors.New("config: at least one backend must have role 'primary'")
	}

	return nil
}
