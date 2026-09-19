package provision

import (
	"strings"
	"testing"

	"plinth/internal/config"
)

func TestModelWeightsProvisionerModelForNode(t *testing.T) {
	models := []config.ModelConfig{
		{Name: "Ring-A-Model", Ring: "ring-a"},
		{Name: "Standalone-Model"},
	}

	tests := []struct {
		name string
		node config.NodeConfig
		want string
	}{
		{
			name: "ring node picks its ring model",
			node: config.NodeConfig{Name: "n1", IP: "10.0.0.1", Ring: "ring-a"},
			want: "Ring-A-Model",
		},
		{
			name: "standalone node picks ring-less model",
			node: config.NodeConfig{Name: "n2", IP: "10.0.0.2"},
			want: "Standalone-Model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewModelWeightsProvisioner(NewWeightManager("/tmp"), SSHConfig{}, models)
			got, err := p.ModelForNode(tt.node)
			if err != nil {
				t.Fatalf("ModelForNode() = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("ModelForNode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModelWeightsProvisionerModelForNodeErrors(t *testing.T) {
	tests := []struct {
		name   string
		models []config.ModelConfig
		node   config.NodeConfig
	}{
		{
			name:   "ring node with no ring model",
			models: []config.ModelConfig{{Name: "Ring-A-Model", Ring: "ring-a"}},
			node:   config.NodeConfig{Name: "n1", Ring: "ring-b"},
		},
		{
			name:   "standalone node with no ring-less model",
			models: []config.ModelConfig{{Name: "Ring-A-Model", Ring: "ring-a"}},
			node:   config.NodeConfig{Name: "n2"},
		},
		{
			name:   "no models at all",
			models: nil,
			node:   config.NodeConfig{Name: "n3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewModelWeightsProvisioner(NewWeightManager("/tmp"), SSHConfig{}, tt.models)
			_, err := p.ModelForNode(tt.node)
			if err == nil {
				t.Fatal("ModelForNode() = nil, want error")
			}
			if !strings.Contains(err.Error(), "no model") {
				t.Errorf("error = %q, want mention of missing model", err)
			}
		})
	}
}
