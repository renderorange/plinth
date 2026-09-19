package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"plinth/internal/config"
)

func TestMainCompiles(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provision binary failed to compile: %v\n%s", err, out)
	}
}

func newTestConfig() *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "standalone-1"},
			{IP: "10.0.0.2", Name: "standalone-2"},
			{IP: "10.0.0.3", Name: "ring-a-1", Ring: "ring-a"},
			{IP: "10.0.0.4", Name: "ring-a-2", Ring: "ring-a"},
		},
		Models: config.ModelsConfig{
			Available: []config.ModelConfig{
				{Name: "Standalone-Model"},
				{Name: "Ring-A-Model", Ring: "ring-a"},
			},
		},
	}
}

func TestPushTargetsAllStandaloneNoDuplicates(t *testing.T) {
	nodes, err := pushTargets(newTestConfig(), "Standalone-Model", true, nil)
	if err != nil {
		t.Fatalf("pushTargets() = %v, want nil", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("pushTargets() = %d targets, want 2 (standalone-1, standalone-2)", len(nodes))
	}
	seen := map[string]bool{}
	for _, n := range nodes {
		if seen[n.Name] {
			t.Errorf("node %q appears more than once", n.Name)
		}
		seen[n.Name] = true
	}
	if !seen["standalone-1"] || !seen["standalone-2"] {
		t.Errorf("targets = %v, want standalone-1 and standalone-2", seen)
	}
}

func TestPushTargetsAllRingModel(t *testing.T) {
	nodes, err := pushTargets(newTestConfig(), "Ring-A-Model", true, nil)
	if err != nil {
		t.Fatalf("pushTargets() = %v, want nil", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("pushTargets() = %d targets, want 2", len(nodes))
	}
	for _, n := range nodes {
		if n.Ring != "ring-a" {
			t.Errorf("node %q (ring %q) targeted for ring-a model", n.Name, n.Ring)
		}
	}
}

func TestPushTargetsAllUnknownModel(t *testing.T) {
	_, err := pushTargets(newTestConfig(), "Unknown/Model", true, nil)
	if err == nil {
		t.Fatal("pushTargets() with unknown model = nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of unknown model", err)
	}
}

func TestPushTargetsNamed(t *testing.T) {
	nodes, err := pushTargets(newTestConfig(), "Any/Model", false, []string{"standalone-1"})
	if err != nil {
		t.Fatalf("pushTargets() = %v, want nil", err)
	}
	if len(nodes) != 1 || nodes[0].Name != "standalone-1" {
		t.Errorf("pushTargets() = %v, want [standalone-1]", nodes)
	}
}

func TestPushTargetsUnknownNode(t *testing.T) {
	_, err := pushTargets(newTestConfig(), "Any/Model", false, []string{"ghost"})
	if err == nil {
		t.Fatal("pushTargets() with unknown node = nil, want error")
	}
}

func TestPushTargetsAllAndNamed(t *testing.T) {
	_, err := pushTargets(newTestConfig(), "Any/Model", true, []string{"standalone-1"})
	if err == nil {
		t.Fatal("pushTargets() combining --all and named nodes = nil, want error")
	}
}

func TestFindNode(t *testing.T) {
	cfg := newTestConfig()
	if n := findNode(cfg, "ring-a-1"); n == nil || n.IP != "10.0.0.3" {
		t.Errorf("findNode(ring-a-1) = %v, want ring-a-1 node", n)
	}
	if n := findNode(cfg, "ghost"); n != nil {
		t.Errorf("findNode(ghost) = %v, want nil", n)
	}
}
