package api

import (
	"plinth/internal/config"
	"plinth/internal/health"
)

// snapshot is an immutable view of the serving configuration plus the monitor
// serving it. Handlers load one snapshot per request, so a config reload can
// swap the snapshot atomically while in-flight requests finish against the
// previous one.
type snapshot struct {
	cfg        *config.Config
	mon        *health.Monitor
	rings      map[string]map[string]bool
	standalone map[string]bool
}

func newSnapshot(cfg *config.Config, mon *health.Monitor) *snapshot {
	rings := make(map[string]map[string]bool)
	standalone := make(map[string]bool)
	for _, n := range cfg.Nodes {
		if n.Ring != "" {
			ips := rings[n.Ring]
			if ips == nil {
				ips = make(map[string]bool)
				rings[n.Ring] = ips
			}
			ips[n.IP] = true
		} else {
			standalone[n.IP] = true
		}
	}
	return &snapshot{
		cfg:        cfg,
		mon:        mon,
		rings:      rings,
		standalone: standalone,
	}
}

func (s *snapshot) filterByRing(states []health.NodeState, modelName string) []health.NodeState {
	pool := s.standalone
	if ring := s.cfg.ModelRing(modelName); ring != "" {
		pool = s.rings[ring]
	}
	var filtered []health.NodeState
	for _, st := range states {
		if pool[st.IP] {
			filtered = append(filtered, st)
		}
	}
	return filtered
}

// filterByModel drops nodes whose discovery state forbids modelName.
// Exact id match. Untried nodes are kept (identity).
func (s *snapshot) filterByModel(states []health.NodeState, modelName string) []health.NodeState {
	var filtered []health.NodeState
	for _, st := range states {
		if st.Models.Allows(modelName) {
			filtered = append(filtered, st)
		}
	}
	return filtered
}

func (s *snapshot) portForNode(ip string) (int, bool) {
	for _, nc := range s.cfg.Nodes {
		if nc.IP == ip {
			return nc.VLLMPort, true
		}
	}
	return 0, false
}
