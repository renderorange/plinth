package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Cluster   ClusterConfig
	Gateway   GatewayConfig
	Nodes     []NodeConfig
	Models    ModelsConfig
	Provision ProvisionConfig
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

type ProvisionConfig struct {
	WeightsDir      string `toml:"weights_dir"`
	SSHKeyPath      string `toml:"ssh_key"`
	SSHUser         string `toml:"ssh_user"`
	SSHPort         int    `toml:"ssh_port"`
	SSHHostKey      string `toml:"ssh_host_key"`
	GPUExporterBin  string `toml:"gpu_exporter_bin"`
	ServiceFilesDir string `toml:"service_files_dir"`
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

	seen := make(map[string]bool)
	for _, n := range c.Nodes {
		if seen[n.IP] {
			return fmt.Errorf("duplicate node IP %q: plinth node identity is IP-based", n.IP)
		}
		seen[n.IP] = true
	}

	if c.Provision.WeightsDir == "" {
		c.Provision.WeightsDir = defaultWeightsDir
	}
	if c.Provision.SSHKeyPath == "" {
		c.Provision.SSHKeyPath = defaultSSHKeyPath
	}
	if c.Provision.SSHUser == "" {
		c.Provision.SSHUser = "root"
	}
	if c.Provision.SSHPort == 0 {
		c.Provision.SSHPort = 22
	}
	if c.Provision.SSHPort < 1 || c.Provision.SSHPort > 65535 {
		return fmt.Errorf("ssh_port must be between 1 and 65535")
	}
	c.Provision.WeightsDir = expandHome(c.Provision.WeightsDir)
	c.Provision.SSHKeyPath = expandHome(c.Provision.SSHKeyPath)
	if c.Provision.GPUExporterBin == "" {
		c.Provision.GPUExporterBin = defaultGPUExporterBin
	}
	if c.Provision.ServiceFilesDir == "" {
		c.Provision.ServiceFilesDir = defaultServiceFilesDir
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

func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
