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

func TestModelDiscoveryAllows(t *testing.T) {
	tests := []struct {
		name  string
		state ModelState
		names []string
		model string
		want  bool
	}{
		{"untried allows anything", ModelsUntried, nil, "any/model", true},
		{"known allows listed", ModelsKnown, []string{"a", "b"}, "a", true},
		{"known rejects unlisted", ModelsKnown, []string{"a", "b"}, "c", false},
		{"known empty names rejects", ModelsKnown, nil, "a", false},
		{"degraded allows last-known-good", ModelsDegraded, []string{"a"}, "a", true},
		{"degraded rejects unlisted", ModelsDegraded, []string{"a"}, "b", false},
		{"known_empty rejects all", ModelsKnownEmpty, nil, "a", false},
		{"expired rejects all", ModelsExpired, []string{"a"}, "a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ModelDiscovery{State: tt.state, Names: tt.names}
			if got := d.Allows(tt.model); got != tt.want {
				t.Errorf("Allows(%q) = %v, want %v (state=%s)", tt.model, got, tt.want, tt.state)
			}
		})
	}
}

func TestModelDiscoveryExclusionReason(t *testing.T) {
	tests := []struct {
		name  string
		state ModelState
		names []string
		model string
		want  string
	}{
		{"untried never excludes", ModelsUntried, nil, "a", ""},
		{"allows returns empty reason", ModelsKnown, []string{"a"}, "a", ""},
		{"known missing", ModelsKnown, []string{"a"}, "b", "missing"},
		{"degraded missing", ModelsDegraded, []string{"a"}, "b", "missing"},
		{"known_empty", ModelsKnownEmpty, nil, "a", "empty"},
		{"expired", ModelsExpired, nil, "a", "expired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ModelDiscovery{State: tt.state, Names: tt.names}
			if got := d.ExclusionReason(tt.model); got != tt.want {
				t.Errorf("ExclusionReason(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestModelDiscoveryAge(t *testing.T) {
	now := time.Now()
	d := ModelDiscovery{}
	if got := d.Age(now); got != 0 {
		t.Errorf("Age with zero FetchedAt = %v, want 0", got)
	}
	d.FetchedAt = now.Add(-2 * time.Second)
	got := d.Age(now)
	if got < time.Second || got > 3*time.Second {
		t.Errorf("Age = %v, want ~2s", got)
	}
}

func TestModelStateString(t *testing.T) {
	tests := []struct {
		state ModelState
		want  string
	}{
		{ModelsUntried, "untried"},
		{ModelsKnown, "known"},
		{ModelsKnownEmpty, "known_empty"},
		{ModelsDegraded, "degraded"},
		{ModelsExpired, "expired"},
		{ModelState(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodeStateHasModels(t *testing.T) {
	n := NodeState{IP: "10.0.0.1", Status: Healthy}
	if n.Models.State != ModelsUntried {
		t.Errorf("zero NodeState Models.State = %v, want ModelsUntried", n.Models.State)
	}
}

func TestRequestElevated(t *testing.T) {
	now := time.Now()
	ttl := 10 * time.Second
	tests := []struct {
		name      string
		state     NodeState
		threshold int
		want      bool
	}{
		{"zero streak", NodeState{}, 3, false},
		{"below threshold", NodeState{ReqStreak: 2, ReqLastHit: now}, 3, false},
		{"at threshold", NodeState{ReqStreak: 3, ReqLastHit: now}, 3, true},
		{"above threshold", NodeState{ReqStreak: 4, ReqLastHit: now}, 3, true},
		{"no last hit", NodeState{ReqStreak: 5}, 3, false},
		{"stale", NodeState{ReqStreak: 5, ReqLastHit: now.Add(-11 * time.Second)}, 3, false},
		{"exactly ttl still elevated", NodeState{ReqStreak: 5, ReqLastHit: now.Add(-10 * time.Second)}, 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.RequestElevated(now, tt.threshold, ttl); got != tt.want {
				t.Errorf("RequestElevated() = %v, want %v", got, tt.want)
			}
		})
	}
}
