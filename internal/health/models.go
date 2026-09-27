package health

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"plinth/internal/config"
	"plinth/internal/log"
	"plinth/internal/metrics"
)

// applyResult folds one fetch outcome into d. failThreshold is
// Gateway.HealthFailThreshold (N). Returns true when Names contents changed.
//
// Rules (spec §3.2):
//   - fetch error keeps Names (last-known-good) and increments Fails
//   - Fails >= failThreshold forces Expired from any non-expired state
//   - successful non-empty list replaces Names and resets counters
//   - two consecutive empty lists are required to enter KnownEmpty
func (d *ModelDiscovery) applyResult(names []string, fetchErr error, failThreshold int, now time.Time) bool {
	prevNames := d.Names

	if fetchErr != nil {
		d.Fails++
		d.Empties = 0
		d.LastError = fetchErr.Error()
		if failThreshold > 0 && d.Fails >= failThreshold {
			d.State = ModelsExpired
			return false
		}
		switch d.State {
		case ModelsKnown, ModelsDegraded:
			d.State = ModelsDegraded
		case ModelsUntried, ModelsKnownEmpty, ModelsExpired:
			// keep current state
		}
		return false
	}

	// Successful fetch.
	d.Fails = 0
	d.LastError = ""
	d.FetchedAt = now

	if len(names) > 0 {
		d.State = ModelsKnown
		d.Names = append([]string(nil), names...)
		d.Empties = 0
		return !stringSlicesEqual(prevNames, d.Names)
	}

	d.Empties++
	if d.Empties >= 2 {
		d.State = ModelsKnownEmpty
		d.Names = nil
		return len(prevNames) > 0
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// parseModelList extracts data[].id from a vLLM GET /v1/models body.
// A valid empty list (data: []) returns a non-nil empty slice and nil error.
// A body without a data array (missing, null, or a non-array) is an error.
func parseModelList(r io.Reader) ([]string, error) {
	var resp modelsResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("parse model list: missing data field")
	}
	names := make([]string, 0, len(resp.Data))
	for _, d := range resp.Data {
		names = append(names, d.ID)
	}
	return names, nil
}

// applyModelsResult updates discovery state for ip after one fetch and emits
// transition logs + metrics. Safe to call concurrently; no-op for unknown IPs.
func (m *Monitor) applyModelsResult(ip string, names []string, fetchErr error) {
	m.mu.Lock()
	node, ok := m.nodes[ip]
	if !ok {
		m.mu.Unlock()
		return
	}
	prev := node.Models
	changed := node.Models.applyResult(names, fetchErr, m.cfg.Gateway.HealthFailThreshold, time.Now())
	next := node.Models
	// Deep-copy names for use outside the lock.
	namesCopy := append([]string(nil), next.Names...)
	nodeCfg := m.nodeConfigLocked(ip)
	m.mu.Unlock()

	switch {
	case fetchErr != nil:
		metrics.ModelDiscoveryTotal.WithLabelValues(ip, "error").Inc()
	case len(names) == 0:
		metrics.ModelDiscoveryTotal.WithLabelValues(ip, "empty").Inc()
	default:
		metrics.ModelDiscoveryTotal.WithLabelValues(ip, "success").Inc()
	}

	if next.State == ModelsKnown || next.State == ModelsKnownEmpty {
		metrics.NodeModelsOK.WithLabelValues(ip).Set(1)
	} else {
		metrics.NodeModelsOK.WithLabelValues(ip).Set(0)
	}

	if next.State != prev.State {
		switch next.State {
		case ModelsKnown:
			log.Info("models discovered", "node", ip)
		case ModelsKnownEmpty:
			log.Warn("node reports no models", "node", ip)
		case ModelsDegraded:
			log.Warn("model discovery failed", "node", ip, "error", next.LastError)
		case ModelsExpired:
			log.Error("model discovery expired", "node", ip, "error", next.LastError)
		}
		if prev.State == ModelsDegraded && next.State == ModelsKnown {
			log.Info("model discovery recovered", "node", ip)
		}
	}
	if changed {
		log.Info("node model list changed", "node", ip)
		warnConfigModelsMissing(nodeCfg, namesCopy, ip, m.cfg)
	}
}

// nodeConfigLocked returns the config.NodeConfig for ip. Caller holds m.mu.
func (m *Monitor) nodeConfigLocked(ip string) config.NodeConfig {
	for _, n := range m.cfg.Nodes {
		if n.IP == ip {
			return n
		}
	}
	return config.NodeConfig{IP: ip}
}

// warnConfigModelsMissing warns once per (node, model) per list change for
// configured models that should be on this node but are not in names. It also
// checks models.default when it is not in available.
func warnConfigModelsMissing(nodeCfg config.NodeConfig, names []string, ip string, cfg *config.Config) {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	check := func(modelName string) {
		if modelName == "" || set[modelName] {
			return
		}
		log.Warn("configured model not served by node", "model", modelName, "node", ip)
	}
	seen := make(map[string]bool)
	for _, am := range cfg.Models.Available {
		seen[am.Name] = true
		if am.Ring == "" {
			if nodeCfg.Ring != "" {
				continue
			}
		} else if am.Ring != nodeCfg.Ring {
			continue
		}
		check(am.Name)
	}
	if cfg.Models.Default != "" && !seen[cfg.Models.Default] {
		check(cfg.Models.Default)
	}
}

func (m *Monitor) listModels(url string) ([]string, error) {
	resp, err := m.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned status %d", resp.StatusCode)
	}
	return parseModelList(resp.Body)
}

func (m *Monitor) fetchModels(nc config.NodeConfig) {
	if !m.tryStartModels(nc.IP) {
		return
	}
	defer m.finishModels(nc.IP)

	url := fmt.Sprintf("http://%s:%d/v1/models", nc.IP, nc.VLLMPort)
	names, err := m.listModels(url)
	m.applyModelsResult(nc.IP, names, err)
}

// discoverAll starts one fetchModels goroutine per node and returns
// immediately. Overlap is skipped via modelsChecking, so a hung fetch cannot
// delay health checks (spec §5.1 / T3).
func (m *Monitor) discoverAll() {
	for _, nc := range m.cfg.Nodes {
		go m.fetchModels(nc)
	}
}
