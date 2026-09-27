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

type NodeState struct {
	IP                  string
	Name                string
	Status              Status
	LastCheck           time.Time
	ConsecutiveFailures int
	GPUs                []GPUMetrics
	Models              ModelDiscovery
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
