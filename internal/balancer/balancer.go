package balancer

import (
	"errors"
	"sync/atomic"

	"distributed-vram/internal/health"
)

var ErrNoHealthyNode = errors.New("no healthy node available")

type Balancer struct {
	counter atomic.Uint64
}

func New() *Balancer {
	return &Balancer{}
}

func (b *Balancer) Select(states []health.NodeState) (*health.NodeState, error) {
	var healthy []health.NodeState
	var degraded []health.NodeState

	for _, s := range states {
		switch s.Status {
		case health.Healthy:
			healthy = append(healthy, s)
		case health.Degraded:
			degraded = append(degraded, s)
		}
	}

	pool := healthy
	if len(pool) == 0 {
		pool = degraded
	}
	if len(pool) == 0 {
		return nil, ErrNoHealthyNode
	}

	idx := b.counter.Add(1) - 1
	selected := pool[idx%uint64(len(pool))]
	return &selected, nil
}
