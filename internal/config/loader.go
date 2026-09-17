package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultListen         = ":8000"
	defaultMetricsListen  = ":9090"
	defaultHealthInterval = 3 * time.Second
	defaultFailThreshold  = 3
	defaultVLLMPort       = 8000
	defaultMetricsPort    = 9100
)

type rawConfig struct {
	Cluster struct {
		Name string
	}
	Gateway struct {
		Listen              string
		MetricsListen       string `toml:"metrics_listen"`
		HealthInterval      string `toml:"health_interval"`
		HealthFailThreshold int    `toml:"health_fail_threshold"`
	}
	Nodes []struct {
		IP          string
		Name        string
		VLLMPort    int `toml:"vllm_port"`
		MetricsPort int `toml:"metrics_port"`
	}
	Models struct {
		Default   string
		Available []struct {
			Name           string
			PipelineStages int `toml:"pipeline_stages"`
		}
	}
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
		Cluster: ClusterConfig{Name: raw.Cluster.Name},
		Gateway: GatewayConfig{
			Listen:              raw.Gateway.Listen,
			MetricsListen:       raw.Gateway.MetricsListen,
			HealthFailThreshold: raw.Gateway.HealthFailThreshold,
		},
		Models: ModelsConfig{
			Default: raw.Models.Default,
		},
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

	for _, n := range raw.Nodes {
		vllmPort := n.VLLMPort
		if vllmPort == 0 {
			vllmPort = defaultVLLMPort
		}
		metricsPort := n.MetricsPort
		if metricsPort == 0 {
			metricsPort = defaultMetricsPort
		}
		cfg.Nodes = append(cfg.Nodes, NodeConfig{
			IP:          n.IP,
			Name:        n.Name,
			VLLMPort:    vllmPort,
			MetricsPort: metricsPort,
		})
	}

	for _, m := range raw.Models.Available {
		cfg.Models.Available = append(cfg.Models.Available, ModelConfig{
			Name:           m.Name,
			PipelineStages: m.PipelineStages,
		})
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
