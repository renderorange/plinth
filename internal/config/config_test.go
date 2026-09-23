package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestProvisionConfig(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Provision: ProvisionConfig{
			WeightsDir: "/var/lib/plinth/weights",
			SSHKeyPath: "/home/user/.ssh/id_rsa",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestProvisionConfigDefaults(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if cfg.Provision.WeightsDir != defaultWeightsDir {
		t.Errorf("WeightsDir = %q, want %q", cfg.Provision.WeightsDir, defaultWeightsDir)
	}
	if wantKey := expandHome(defaultSSHKeyPath); cfg.Provision.SSHKeyPath != wantKey {
		t.Errorf("SSHKeyPath = %q, want %q", cfg.Provision.SSHKeyPath, wantKey)
	}
	if cfg.Provision.SSHUser != "root" {
		t.Errorf("SSHUser = %q, want %q", cfg.Provision.SSHUser, "root")
	}
	if cfg.Provision.SSHPort != 22 {
		t.Errorf("SSHPort = %d, want 22", cfg.Provision.SSHPort)
	}
}

func TestProvisionDefaultPathsExpandTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	wantKey := filepath.Join(home, ".ssh", "id_rsa")
	if cfg.Provision.SSHKeyPath != wantKey {
		t.Errorf("SSHKeyPath = %q, want %q", cfg.Provision.SSHKeyPath, wantKey)
	}

	cfg2 := &Config{
		Gateway: GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Provision: ProvisionConfig{
			WeightsDir:      "~/weights",
			SSHKeyPath:      "//home/custom/key",
			GPUExporterBin:  "~/bin/gpu-exporter",
			ServiceFilesDir: "~/plinth/scripts",
		},
	}
	if err := cfg2.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if cfg2.Provision.WeightsDir != filepath.Join(home, "weights") {
		t.Errorf("WeightsDir = %q, want %q", cfg2.Provision.WeightsDir, filepath.Join(home, "weights"))
	}
	if cfg2.Provision.SSHKeyPath != "//home/custom/key" {
		t.Errorf("SSHKeyPath = %q, want unchanged absolute path", cfg2.Provision.SSHKeyPath)
	}
	if cfg2.Provision.GPUExporterBin != filepath.Join(home, "bin", "gpu-exporter") {
		t.Errorf("GPUExporterBin = %q, want %q", cfg2.Provision.GPUExporterBin, filepath.Join(home, "bin", "gpu-exporter"))
	}
	if cfg2.Provision.ServiceFilesDir != filepath.Join(home, "plinth", "scripts") {
		t.Errorf("ServiceFilesDir = %q, want %q", cfg2.Provision.ServiceFilesDir, filepath.Join(home, "plinth", "scripts"))
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

func TestValidateDuplicateNodeIPs(t *testing.T) {
	cfg := &Config{
		Gateway: GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", VLLMPort: 8000, MetricsPort: 9100},
			{IP: "10.0.0.1", Name: "node-2", VLLMPort: 8001, MetricsPort: 9101},
		},
		Models: ModelsConfig{
			Available: []ModelConfig{{Name: "test/model", PipelineStages: 1}},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for duplicate node IPs, got nil")
	} else if !strings.Contains(err.Error(), "duplicate node IP") {
		t.Errorf("error = %q, want message containing %q", err.Error(), "duplicate node IP")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	tmp, err := os.CreateTemp("", "gateway-*.toml")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmp.Name()) })
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	return tmp.Name()
}

func TestProvisionConfigServiceDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[gateway]
listen = ":0"
metrics_listen = ":0"

[[nodes]]
ip = "10.0.0.1"
name = "n1"
`))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Provision.GPUExporterBin != "./gpu-exporter" {
		t.Errorf("GPUExporterBin = %q, want %q", cfg.Provision.GPUExporterBin, "./gpu-exporter")
	}
	if cfg.Provision.ServiceFilesDir != "./scripts" {
		t.Errorf("ServiceFilesDir = %q, want %q", cfg.Provision.ServiceFilesDir, "./scripts")
	}
}

func TestProvisionConfigServiceExplicit(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[gateway]
listen = ":0"
metrics_listen = ":0"

[[nodes]]
ip = "10.0.0.1"
name = "n1"

[provision]
gpu_exporter_bin = "/usr/local/bin/gpu-exporter"
service_files_dir = "/opt/plinth/scripts"
`))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Provision.GPUExporterBin != "/usr/local/bin/gpu-exporter" {
		t.Errorf("GPUExporterBin = %q, want %q", cfg.Provision.GPUExporterBin, "/usr/local/bin/gpu-exporter")
	}
	if cfg.Provision.ServiceFilesDir != "/opt/plinth/scripts" {
		t.Errorf("ServiceFilesDir = %q, want %q", cfg.Provision.ServiceFilesDir, "/opt/plinth/scripts")
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
