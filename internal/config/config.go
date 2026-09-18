package config

import (
	"fmt"
	"time"
)

type Config struct {
	Cluster ClusterConfig
	Gateway GatewayConfig
	Nodes   []NodeConfig
	Models  ModelsConfig
}

type ClusterConfig struct {
	Name string
}

type GatewayConfig struct {
	Listen              string
	MetricsListen       string
	HealthInterval      time.Duration
	HealthFailThreshold int
}

type NodeConfig struct {
	IP          string
	Name        string
	VLLMPort    int    `toml:"vllm_port"`
	MetricsPort int    `toml:"metrics_port"`
	Ring        string `toml:"ring"`
}

type ModelsConfig struct {
	Default   string
	Available []ModelConfig
}

type ModelConfig struct {
	Name           string
	PipelineStages int    `toml:"pipeline_stages"`
	Ring           string `toml:"ring"`
}

func (c *Config) Validate() error {
	if c.Gateway.HealthInterval <= 0 {
		return fmt.Errorf("health_interval must be greater than zero")
	}
	if c.Gateway.HealthFailThreshold <= 0 {
		return fmt.Errorf("health_fail_threshold must be greater than zero")
	}
	if len(c.Nodes) == 0 {
		return fmt.Errorf("at least one node must be configured")
	}

	ringMembers := make(map[string]bool)
	hasStandaloneNode := false
	for _, n := range c.Nodes {
		if n.Ring != "" {
			ringMembers[n.Ring] = true
		} else {
			hasStandaloneNode = true
		}
	}
	for _, m := range c.Models.Available {
		if m.Ring != "" {
			if !ringMembers[m.Ring] {
				return fmt.Errorf("model %q references ring %q, but no node belongs to that ring", m.Name, m.Ring)
			}
		} else if !hasStandaloneNode {
			return fmt.Errorf("model %q has no ring, but every node belongs to a ring", m.Name)
		}
	}

	return nil
}

func (c *Config) NodesInRing(ring string) []NodeConfig {
	var nodes []NodeConfig
	for _, n := range c.Nodes {
		if n.Ring == ring {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func (c *Config) ModelRing(modelName string) string {
	for _, m := range c.Models.Available {
		if m.Name == modelName {
			return m.Ring
		}
	}
	return ""
}
