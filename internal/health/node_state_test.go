package health

import (
	"testing"
	"time"
)

func TestNodeStatusTransitions(t *testing.T) {
	tests := []struct {
		name                string
		consecutiveFailures int
		threshold           int
		want                Status
	}{
		{"no failures", 0, 3, Healthy},
		{"1 failure degraded", 1, 3, Degraded},
		{"2 failures degraded", 2, 3, Degraded},
		{"3 failures dead", 3, 3, Dead},
		{"4 failures dead", 4, 3, Dead},
		{"threshold 1 immediate dead", 1, 1, Dead},
		{"threshold 2 one failure", 1, 2, Degraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeStatus(tt.consecutiveFailures, tt.threshold)
			if got != tt.want {
				t.Errorf("computeStatus(%d, %d) = %v, want %v", tt.consecutiveFailures, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{Healthy, "healthy"},
		{Degraded, "degraded"},
		{Dead, "dead"},
		{Status(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.status.String()
			if got != tt.want {
				t.Errorf("Status(%d).String() = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestComputeStatusThresholdZero(t *testing.T) {
	// threshold=0: consecutiveFailures >= 0 is always true → Dead
	got := computeStatus(0, 0)
	if got != Dead {
		t.Errorf("computeStatus(0, 0) = %v, want Dead", got)
	}
	got = computeStatus(1, 0)
	if got != Dead {
		t.Errorf("computeStatus(1, 0) = %v, want Dead", got)
	}
}

func TestNodeStateTimestamp(t *testing.T) {
	n := NodeState{
		IP:        "10.0.0.1",
		Name:      "node-1",
		Status:    Healthy,
		LastCheck: time.Now(),
	}
	if n.LastCheck.IsZero() {
		t.Error("LastCheck should not be zero")
	}
}

func TestNodeStateGPUsField(t *testing.T) {
	node := NodeState{
		IP:   "10.0.0.1",
		Name: "gpu-node-1",
		GPUs: []GPUMetrics{
			{UUID: "GPU-aaa", MemoryUsed: 100, MemoryTotal: 200, Utilization: 50, Temperature: 60},
			{UUID: "GPU-bbb", MemoryUsed: 300, MemoryTotal: 400, Utilization: 70, Temperature: 80},
		},
	}
	if len(node.GPUs) != 2 {
		t.Fatalf("expected 2 GPUs, got %d", len(node.GPUs))
	}
	if node.GPUs[0].UUID != "GPU-aaa" {
		t.Errorf("expected GPU-aaa, got %s", node.GPUs[0].UUID)
	}
	if node.GPUs[1].UUID != "GPU-bbb" {
		t.Errorf("expected GPU-bbb, got %s", node.GPUs[1].UUID)
	}
}
