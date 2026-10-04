package health

import "time"

type Status int

const (
	Healthy Status = iota
	Degraded
	Dead
)

func (s Status) String() string {
	switch s {
	case Healthy:
		return "healthy"
	case Degraded:
		return "degraded"
	case Dead:
		return "dead"
	default:
		return "unknown"
	}
}

type GPUMetrics struct {
	UUID        string
	MemoryUsed  uint64
	MemoryTotal uint64
	Utilization uint32
	Temperature uint32
}

// ModelState is per-node model-discovery progress.
type ModelState int

const (
	// ModelsUntried means zero successful fetches and Fails < N.
	// Allows is the identity (include all models).
	ModelsUntried ModelState = iota
	ModelsKnown
	ModelsKnownEmpty
	ModelsDegraded
	ModelsExpired
)

func (s ModelState) String() string {
	switch s {
	case ModelsUntried:
		return "untried"
	case ModelsKnown:
		return "known"
	case ModelsKnownEmpty:
		return "known_empty"
	case ModelsDegraded:
		return "degraded"
	case ModelsExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// ModelDiscovery is last-known-good listing state for one node.
type ModelDiscovery struct {
	State     ModelState
	Names     []string  // last successful non-empty list; nil if none
	FetchedAt time.Time // last successful fetch; zero if never
	Fails     int       // consecutive failed fetches since last success
	Empties   int       // consecutive successful [] responses
	LastError string    // last fetch error text; "" if none
}

// Allows reports whether the node may receive requests for model.
// Matching is exact string equality. Untried nodes allow all models.
func (d ModelDiscovery) Allows(model string) bool {
	switch d.State {
	case ModelsUntried:
		return true
	case ModelsKnown, ModelsDegraded:
		for _, n := range d.Names {
			if n == model {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// ExclusionReason returns the metrics reason label when Allows is false.
func (d ModelDiscovery) ExclusionReason(model string) string {
	if d.Allows(model) {
		return ""
	}
	switch d.State {
	case ModelsKnownEmpty:
		return "empty"
	case ModelsExpired:
		return "expired"
	case ModelsKnown, ModelsDegraded:
		return "missing"
	default:
		return ""
	}
}

// Age is time since the last successful fetch; 0 if never fetched.
func (d ModelDiscovery) Age(now time.Time) time.Duration {
	if d.FetchedAt.IsZero() {
		return 0
	}
	return now.Sub(d.FetchedAt)
}

// Clone returns a deep copy of d. The returned value shares no slice backing
// arrays with d.
func (d ModelDiscovery) Clone() ModelDiscovery {
	out := d
	if d.Names != nil {
		out.Names = append([]string(nil), d.Names...)
	}
	return out
}

// NodeState is one node's last-observed health state.
//
// Ownership: values returned by GetNodeStates are fully owned by the caller
// (deep-copied via Clone) and may be mutated freely. NewMonitorWithState
// deep-copies everything it carries from prev. The monitor never mutates
// published slice elements in place; updates replace whole slices.
type NodeState struct {
	IP                  string
	Name                string
	Status              Status
	LastCheck           time.Time
	ConsecutiveFailures int
	// ReqStreak is the consecutive request-level 5xx outcomes (ReportOutcome
	// hits) observed for this node. It drives request elevation: a streak of
	// requestOverloadThreshold or more changes the served status to Degraded
	// until the streak clears (Task 3) or decays (time-based, read-evaluated).
	ReqStreak int
	// ReqLastHit is the time of the most recent streak increment. Together
	// with ReqStreak it determines request elevation; see RequestElevated.
	ReqLastHit time.Time
	GPUs       []GPUMetrics
	Models     ModelDiscovery
}

// Clone returns a deep copy of n. The returned value shares no slice backing
// arrays with n. See the NodeState ownership rules.
func (n NodeState) Clone() NodeState {
	out := n
	if n.GPUs != nil {
		out.GPUs = append([]GPUMetrics(nil), n.GPUs...)
	}
	out.Models = n.Models.Clone()
	return out
}

// RequestElevated reports whether n's request-outcome streak currently
// elevates the node's served status to Degraded. Elevation caps at Degraded
// and is bounded by ttl since the last hit: a drained node (no traffic to
// clear the streak) recovers by not being hit again for ttl.
func (n NodeState) RequestElevated(now time.Time, threshold int, ttl time.Duration) bool {
	if n.ReqStreak < threshold {
		return false
	}
	return !n.ReqLastHit.IsZero() && now.Sub(n.ReqLastHit) <= ttl
}

func computeStatus(consecutiveFailures, threshold int) Status {
	if consecutiveFailures >= threshold {
		return Dead
	}
	degradedThreshold := threshold / 2
	if degradedThreshold < 1 {
		degradedThreshold = 1
	}
	if consecutiveFailures >= degradedThreshold {
		return Degraded
	}
	return Healthy
}
