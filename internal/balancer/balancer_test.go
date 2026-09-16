package balancer

import (
	"sync"
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

func TestSelectEmptyStates(t *testing.T) {
	b := New()
	_, err := b.Select("test/model", []health.NodeState{})
	if err != ErrNoHealthyNode {
		t.Errorf("expected ErrNoHealthyNode, got %v", err)
	}
}

func TestSelectRoundRobinWraparound(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
		{IP: "10.0.0.3", Name: "node-3", Status: health.Healthy},
	}

	seen := make(map[string]bool)
	for i := 0; i < 6; i++ {
		node, err := b.Select("test/model", states)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		seen[node.IP] = true
	}

	// After 6 calls with 3 nodes, each node should be selected at least once
	if len(seen) != 3 {
		t.Errorf("expected all 3 nodes seen, got %d: %v", len(seen), seen)
	}
}

func TestSelectMixedPoolThreeNodes(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Dead},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
		{IP: "10.0.0.3", Name: "node-3", Status: health.Degraded},
	}

	// Should pick the healthy node, skipping dead and degraded
	node, err := b.Select("test/model", states)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.IP != "10.0.0.2" {
		t.Errorf("selected %s, want 10.0.0.2 (only healthy)", node.IP)
	}
}

func TestSelectConcurrentSafety(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node, err := b.Select("test/model", states)
			if err != nil {
				t.Errorf("concurrent select: unexpected error: %v", err)
			}
			if node == nil {
				t.Error("concurrent select: got nil node")
			}
		}()
	}
	wg.Wait()
}
