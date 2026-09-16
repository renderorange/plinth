package balancer

import (
	"testing"

	"distributed-vram/internal/health"
)

func TestSelectHealthyNode(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	node, err := b.Select("test/model", states)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node == nil {
		t.Fatal("expected a node, got nil")
	}
}

func TestSelectSkipsDeadNodes(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Dead},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	node, err := b.Select("test/model", states)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.IP != "10.0.0.2" {
		t.Errorf("selected %s, want 10.0.0.2", node.IP)
	}
}

func TestSelectAllDead(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Dead},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Dead},
	}

	_, err := b.Select("test/model", states)
	if err == nil {
		t.Fatal("expected error when all nodes dead")
	}
}

func TestSelectRoundRobin(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	// First call should select one node, second call the other
	n1, _ := b.Select("test/model", states)
	n2, _ := b.Select("test/model", states)
	if n1.IP == n2.IP {
		t.Errorf("round robin returned same node twice: %s", n1.IP)
	}
}

func TestSelectDeprioritizesDegraded(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Degraded},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	// Should prefer healthy over degraded
	node, _ := b.Select("test/model", states)
	if node.IP != "10.0.0.2" {
		t.Errorf("selected %s, want 10.0.0.2 (healthy preferred over degraded)", node.IP)
	}
}

func TestSelectDegradedWhenNoHealthy(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Degraded},
	}

	// Should use degraded node if no healthy available
	node, err := b.Select("test/model", states)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.IP != "10.0.0.1" {
		t.Errorf("selected %s, want 10.0.0.1 (degraded fallback)", node.IP)
	}
}
