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
