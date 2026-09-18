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

type NodeState struct {
	IP                  string
	Name                string
	Status              Status
	LastCheck           time.Time
	ConsecutiveFailures int
	GPUs                []GPUMetrics
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
