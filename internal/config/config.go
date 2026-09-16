package config

import "time"

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
	VLLMPort    int `toml:"vllm_port"`
	MetricsPort int `toml:"metrics_port"`
}

type ModelsConfig struct {
	Default   string
	Available []ModelConfig
}

type ModelConfig struct {
	Name           string
	PipelineStages int `toml:"pipeline_stages"`
}
