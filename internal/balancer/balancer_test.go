package balancer

import (
	"fmt"
	"sync"
	"testing"

	"plinth/internal/health"
)

func TestSelectHealthyNode(t *testing.T) {
	b := New()
	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	node, err := b.Select(states)
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

	node, err := b.Select(states)
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

	_, err := b.Select(states)
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
	n1, _ := b.Select(states)
	n2, _ := b.Select(states)
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
	node, _ := b.Select(states)
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
	node, err := b.Select(states)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if node.IP != "10.0.0.1" {
		t.Errorf("selected %s, want 10.0.0.1 (degraded fallback)", node.IP)
	}
}

func TestSelectEmptyStates(t *testing.T) {
	b := New()
	_, err := b.Select([]health.NodeState{})
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

	expected := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.1", "10.0.0.2", "10.0.0.3"}
	for i, want := range expected {
		node, err := b.Select(states)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if node.IP != want {
			t.Errorf("iteration %d: got %s, want %s", i, node.IP, want)
		}
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
	node, err := b.Select(states)
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

	var mu sync.Mutex
	var errs []string
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node, err := b.Select(states)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Sprintf("concurrent select: unexpected error: %v", err))
			}
			if node == nil {
				errs = append(errs, "concurrent select: got nil node")
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent errors: %v", errs)
	}
}
