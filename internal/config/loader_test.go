package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadGatewayConfig(t *testing.T) {
	tests := []struct {
		name      string
		toml      string
		wantErr   bool
		validate  func(*testing.T, *Config)
	}{
		{
			name: "valid full config",
			toml: `
[cluster]
name = "test-cluster"

[gateway]
listen = ":9000"
metrics_listen = ":9091"
health_interval = "5s"
health_fail_threshold = 2

[[nodes]]
ip = "10.0.0.1"
name = "node-1"
vllm_port = 8000
metrics_port = 9100

[[nodes]]
ip = "10.0.0.2"
name = "node-2"
vllm_port = 8000
metrics_port = 9100

[models]
default = "test/model"

[[models.available]]
name = "test/model"
pipeline_stages = 2
`,
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				if cfg.Cluster.Name != "test-cluster" {
					t.Errorf("cluster name = %q, want %q", cfg.Cluster.Name, "test-cluster")
				}
				if cfg.Gateway.Listen != ":9000" {
					t.Errorf("listen = %q, want %q", cfg.Gateway.Listen, ":9000")
				}
				if cfg.Gateway.HealthInterval != 5*time.Second {
					t.Errorf("health_interval = %v, want %v", cfg.Gateway.HealthInterval, 5*time.Second)
				}
				if cfg.Gateway.HealthFailThreshold != 2 {
					t.Errorf("health_fail_threshold = %d, want %d", cfg.Gateway.HealthFailThreshold, 2)
				}
				if len(cfg.Nodes) != 2 {
					t.Fatalf("nodes count = %d, want 2", len(cfg.Nodes))
				}
				if cfg.Nodes[0].IP != "10.0.0.1" {
					t.Errorf("node[0].ip = %q, want %q", cfg.Nodes[0].IP, "10.0.0.1")
				}
				if cfg.Models.Default != "test/model" {
					t.Errorf("models.default = %q, want %q", cfg.Models.Default, "test/model")
				}
				if len(cfg.Models.Available) != 1 {
					t.Fatalf("models.available count = %d, want 1", len(cfg.Models.Available))
				}
				if cfg.Models.Available[0].PipelineStages != 2 {
					t.Errorf("pipeline_stages = %d, want %d", cfg.Models.Available[0].PipelineStages, 2)
				}
			},
		},
		{
			name: "missing health_interval",
			toml: `
[cluster]
name = "test-cluster"

[gateway]
listen = ":9000"
metrics_listen = ":9091"
health_fail_threshold = 2

[models]
default = "test/model"
`,
			wantErr: true,
		},
		{
			name:    "invalid TOML",
			toml:    `this is not valid toml [[[`,
			wantErr: true,
		},
		{
			name:    "missing file",
			toml:    "",
			wantErr: true,
		},
		{
			name: "zero nodes",
			toml: `
[cluster]
name = "test-cluster"

[gateway]
listen = ":9000"
metrics_listen = ":9091"
health_interval = "5s"
health_fail_threshold = 2

[models]
default = "test/model"
`,
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				if len(cfg.Nodes) != 0 {
					t.Errorf("nodes count = %d, want 0", len(cfg.Nodes))
				}
			},
		},
		{
			name: "multiple models",
			toml: `
[cluster]
name = "test-cluster"

[gateway]
listen = ":9000"
metrics_listen = ":9091"
health_interval = "5s"
health_fail_threshold = 2

[models]
default = "model-a"

[[models.available]]
name = "model-a"
pipeline_stages = 1

[[models.available]]
name = "model-b"
pipeline_stages = 2
`,
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				if len(cfg.Models.Available) != 2 {
					t.Fatalf("models count = %d, want 2", len(cfg.Models.Available))
				}
				if cfg.Models.Available[0].Name != "model-a" {
					t.Errorf("model[0] = %q, want model-a", cfg.Models.Available[0].Name)
				}
				if cfg.Models.Available[1].Name != "model-b" {
					t.Errorf("model[1] = %q, want model-b", cfg.Models.Available[1].Name)
				}
				if cfg.Models.Available[1].PipelineStages != 2 {
					t.Errorf("model[1].pipeline_stages = %d, want 2", cfg.Models.Available[1].PipelineStages)
				}
			},
		},
		{
			name: "invalid health_interval format",
			toml: `
[cluster]
name = "test-cluster"

[gateway]
listen = ":9000"
metrics_listen = ":9091"
health_interval = "5"
health_fail_threshold = 2

[models]
default = "test/model"
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var path string

			if tt.name == "missing file" {
				path = "/nonexistent/path.toml"
			} else {
				tmp, err := os.CreateTemp("", "gateway-*.toml")
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(tmp.Name())
				if _, err := tmp.WriteString(tt.toml); err != nil {
					t.Fatal(err)
				}
				tmp.Close()
				path = tmp.Name()
			}

			cfg, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

func TestWatchNotImplemented(t *testing.T) {
	err := Watch("/some/path.toml", func(*Config) {})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
