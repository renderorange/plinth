package config

import (
	"fmt"
	"os"
	"time"

	"github.com/pelletier/go-toml/v2"
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

	interval, err := time.ParseDuration(raw.Gateway.HealthInterval)
	if err != nil {
		return nil, fmt.Errorf("parsing health_interval: %w", err)
	}

	cfg := &Config{
		Cluster: ClusterConfig{Name: raw.Cluster.Name},
		Gateway: GatewayConfig{
			Listen:              raw.Gateway.Listen,
			MetricsListen:       raw.Gateway.MetricsListen,
			HealthInterval:      interval,
			HealthFailThreshold: raw.Gateway.HealthFailThreshold,
		},
		Models: ModelsConfig{
			Default: raw.Models.Default,
		},
	}

	for _, n := range raw.Nodes {
		cfg.Nodes = append(cfg.Nodes, NodeConfig{
			IP:          n.IP,
			Name:        n.Name,
			VLLMPort:    n.VLLMPort,
			MetricsPort: n.MetricsPort,
		})
	}

	for _, m := range raw.Models.Available {
		cfg.Models.Available = append(cfg.Models.Available, ModelConfig{
			Name:           m.Name,
			PipelineStages: m.PipelineStages,
		})
	}

	return cfg, nil
}

func Watch(path string, callback func(*Config)) error {
	return fmt.Errorf("not implemented")
}
