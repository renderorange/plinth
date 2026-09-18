package config

import (
	"testing"
	"time"
)

func TestValidateValidConfig(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", VLLMPort: 8000, MetricsPort: 9100},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestValidateZeroInterval(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      0,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero health_interval")
	}
}

func TestValidateNegativeInterval(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      -1 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative health_interval")
	}
}

func TestValidateZeroThreshold(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 0,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero health_fail_threshold")
	}
}

func TestValidateNegativeThreshold(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: -1,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for negative health_fail_threshold")
	}
}

func TestValidateNoNodes(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for zero nodes")
	}
}

func TestValidateNilNodes(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: nil,
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for nil nodes")
	}
}

func TestDefaultListen(t *testing.T) {
	if defaultListen != ":8000" {
		t.Errorf("defaultListen = %q, want :8000", defaultListen)
	}
}

func TestDefaultMetricsListen(t *testing.T) {
	if defaultMetricsListen != ":9090" {
		t.Errorf("defaultMetricsListen = %q, want :9090", defaultMetricsListen)
	}
}

func TestDefaultVLLMPort(t *testing.T) {
	if defaultVLLMPort != 8000 {
		t.Errorf("defaultVLLMPort = %d, want 8000", defaultVLLMPort)
	}
}

func TestDefaultMetricsPort(t *testing.T) {
	if defaultMetricsPort != 9100 {
		t.Errorf("defaultMetricsPort = %d, want 9100", defaultMetricsPort)
	}
}

func TestDefaultHealthInterval(t *testing.T) {
	if defaultHealthInterval != 3*time.Second {
		t.Errorf("defaultHealthInterval = %v, want 3s", defaultHealthInterval)
	}
}

func TestDefaultFailThreshold(t *testing.T) {
	if defaultFailThreshold != 3 {
		t.Errorf("defaultFailThreshold = %d, want 3", defaultFailThreshold)
	}
}

func TestValidateRingReferencesValidRing(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", Ring: "ring-a"},
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-a"},
		},
		Models: ModelsConfig{
			Default: "big/model",
			Available: []ModelConfig{
				{Name: "big/model", PipelineStages: 2, Ring: "ring-a"},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestValidateRingReferencesInvalidRing(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Models: ModelsConfig{
			Default: "big/model",
			Available: []ModelConfig{
				{Name: "big/model", PipelineStages: 2, Ring: "nonexistent"},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for model referencing nonexistent ring")
	}
}

func TestValidateModelWithoutRing(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Models: ModelsConfig{
			Default: "small/model",
			Available: []ModelConfig{
				{Name: "small/model", PipelineStages: 1},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for model without ring, got %v", err)
	}
}

func TestValidateStandaloneModelWithNoUngroupedNodes(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", Ring: "ring-a"},
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-a"},
		},
		Models: ModelsConfig{
			Default: "small/model",
			Available: []ModelConfig{
				{Name: "small/model", PipelineStages: 1},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for standalone model when every node belongs to a ring")
	}
}

func TestNodesInRing(t *testing.T) {
	cfg := &Config{
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", Ring: "ring-a"},
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-a"},
			{IP: "10.0.0.3", Name: "node-3", Ring: "ring-b"},
			{IP: "10.0.0.4", Name: "node-4"},
		},
	}
	nodes := cfg.NodesInRing("ring-a")
	if len(nodes) != 2 {
		t.Errorf("NodesInRing(ring-a) = %d, want 2", len(nodes))
	}
	nodes = cfg.NodesInRing("ring-b")
	if len(nodes) != 1 {
		t.Errorf("NodesInRing(ring-b) = %d, want 1", len(nodes))
	}
	nodes = cfg.NodesInRing("nonexistent")
	if len(nodes) != 0 {
		t.Errorf("NodesInRing(nonexistent) = %d, want 0", len(nodes))
	}
}

func TestModelRing(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Default: "small/model",
			Available: []ModelConfig{
				{Name: "big/model", PipelineStages: 2, Ring: "ring-a"},
				{Name: "small/model", PipelineStages: 1},
			},
		},
	}
	if ring := cfg.ModelRing("big/model"); ring != "ring-a" {
		t.Errorf("ModelRing(big/model) = %q, want ring-a", ring)
	}
	if ring := cfg.ModelRing("small/model"); ring != "" {
		t.Errorf("ModelRing(small/model) = %q, want empty", ring)
	}
	if ring := cfg.ModelRing("nonexistent"); ring != "" {
		t.Errorf("ModelRing(nonexistent) = %q, want empty", ring)
	}
}
