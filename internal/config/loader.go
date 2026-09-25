package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultListen          = ":8000"
	defaultMetricsListen   = ":9090"
	defaultHealthInterval  = 3 * time.Second
	defaultFailThreshold   = 3
	defaultVLLMPort        = 8000
	defaultMetricsPort     = 9100
	defaultWeightsDir      = "/var/lib/plinth/weights"
	defaultSSHKeyPath      = "~/.ssh/id_rsa"
	defaultGPUExporterBin  = "./gpu-exporter"
	defaultServiceFilesDir = "./scripts"

	defaultMaxBufferedResponseBytes int64 = 8 << 20
)

type rawConfig struct {
	Cluster   ClusterConfig
	Gateway   rawGatewayConfig
	Nodes     []NodeConfig
	Models    ModelsConfig
	Provision ProvisionConfig
}

type rawGatewayConfig struct {
	Listen                   string
	MetricsListen            string `toml:"metrics_listen"`
	HealthInterval           string `toml:"health_interval"`
	HealthFailThreshold      int    `toml:"health_fail_threshold"`
	MaxBufferedResponseBytes int64  `toml:"max_buffered_response_bytes"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var raw rawConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg := &Config{
		Cluster: raw.Cluster,
		Gateway: GatewayConfig{
			Listen:                   raw.Gateway.Listen,
			MetricsListen:            raw.Gateway.MetricsListen,
			HealthFailThreshold:      raw.Gateway.HealthFailThreshold,
			MaxBufferedResponseBytes: raw.Gateway.MaxBufferedResponseBytes,
		},
		Nodes:     raw.Nodes,
		Models:    raw.Models,
		Provision: raw.Provision,
	}

	for i := range cfg.Nodes {
		if cfg.Nodes[i].VLLMPort == 0 {
			cfg.Nodes[i].VLLMPort = defaultVLLMPort
		}
		if cfg.Nodes[i].MetricsPort == 0 {
			cfg.Nodes[i].MetricsPort = defaultMetricsPort
		}
	}

	if cfg.Gateway.Listen == "" {
		cfg.Gateway.Listen = defaultListen
	}
	if cfg.Gateway.MetricsListen == "" {
		cfg.Gateway.MetricsListen = defaultMetricsListen
	}
	if cfg.Gateway.HealthFailThreshold == 0 {
		cfg.Gateway.HealthFailThreshold = defaultFailThreshold
	}

	if raw.Gateway.HealthInterval == "" {
		cfg.Gateway.HealthInterval = defaultHealthInterval
	} else {
		interval, err := time.ParseDuration(raw.Gateway.HealthInterval)
		if err != nil {
			return nil, fmt.Errorf("parsing health_interval: %w", err)
		}
		cfg.Gateway.HealthInterval = interval
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
