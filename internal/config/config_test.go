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
