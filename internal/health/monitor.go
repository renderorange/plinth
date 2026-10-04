package health

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"plinth/internal/config"
	"plinth/internal/log"
	"plinth/internal/metrics"
)

// overloadRecoveryTTL bounds how long an untouched request-streak elevates a
// node. The monitor never sees traffic for a drained node, so recovery must
// not depend on receiving a request; the streak decays at read time once it
// has not been hit for this long.
const overloadRecoveryTTL = 10 * time.Second

type Monitor struct {
	cfg            *config.Config
	client         *http.Client
	nodes          map[string]*NodeState
	checking       map[string]bool
	modelsChecking map[string]bool
	mu             sync.RWMutex
	stop           chan struct{}
	done           chan struct{}
	stopOnce       sync.Once
}

func NewMonitor(cfg *config.Config) *Monitor {
	nodes := make(map[string]*NodeState)
	for _, n := range cfg.Nodes {
		nodes[n.IP] = &NodeState{
			IP:     n.IP,
			Name:   n.Name,
			Status: Healthy,
			Models: ModelDiscovery{State: ModelsUntried},
		}
	}
	return &Monitor{
		cfg: cfg,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		nodes:          nodes,
		checking:       make(map[string]bool),
		modelsChecking: make(map[string]bool),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
}

// NewMonitorWithState builds a monitor like NewMonitor but seeds state for IPs
// present in both the new config and prev: Status, ConsecutiveFailures, and
// Models carry over so a reload does not give flapping nodes a clean slate.
// Carried Models are deep-copied (Clone) so the new monitor never shares a
// backing array with prev. GPUs are deliberately not carried over; they
// repopulate from the first scrape after the reload. Nodes not present in prev
// start Healthy and untried, matching startup semantics.
func NewMonitorWithState(cfg *config.Config, prev []NodeState) *Monitor {
	m := NewMonitor(cfg)
	prevByIP := make(map[string]NodeState, len(prev))
	for _, p := range prev {
		prevByIP[p.IP] = p
	}
	for ip, n := range m.nodes {
		if old, ok := prevByIP[ip]; ok {
			n.Status = old.Status
			n.ConsecutiveFailures = old.ConsecutiveFailures
			n.ReqStreak = old.ReqStreak
			n.ReqLastHit = old.ReqLastHit
			n.Models = old.Models.Clone()
		}
	}
	return m
}

func (m *Monitor) Start() {
	go m.loop()
}

// Stop halts the health-check loop and waits for it to exit. It is safe to
// call multiple times, including concurrently: the monitor is torn down
// exactly once.
func (m *Monitor) Stop() {
	m.stopOnce.Do(func() {
		close(m.stop)
		<-m.done
	})
}

func (m *Monitor) GetNodeStates() []NodeState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make([]NodeState, 0, len(m.nodes))
	now := time.Now()
	for _, n := range m.nodes {
		s := n.Clone()
		// Request elevation caps at Degraded and never overrides a probe
		// verdict: a 503 is a traffic signal, only probes may declare death.
		if s.Status == Healthy && s.RequestElevated(now, m.cfg.Gateway.RequestOverloadThreshold(), overloadRecoveryTTL) {
			s.Status = Degraded
		}
		states = append(states, s)
	}
	sort.Slice(states, func(i, j int) bool {
		return states[i].IP < states[j].IP
	})
	return states
}

// Recheck runs an immediate health check for one node. It is used when the
// gateway observes a proxy-level connection failure so that node state
// converges immediately instead of on the polling cadence. Safe to call
// without Start(); no-op for unknown IPs.
func (m *Monitor) Recheck(ip string) {
	var nc config.NodeConfig
	found := false
	for _, n := range m.cfg.Nodes {
		if n.IP == ip {
			nc = n
			found = true
			break
		}
	}
	if !found {
		return
	}
	go m.runCheck(nc)
}

// ReportOutcome records the HTTP status of one proxied attempt against a
// node. status >= 500 is a request-level failure ("the node said no"); any
// other status proves the node served the request and clears the streak.
// The caller does not use the return value; reports are serialized under the
// monitor lock. No-op for unknown IPs. Transport failures (no status) are
// handled by the connection-failure path and must not be reported here.
func (m *Monitor) ReportOutcome(ip string, status int) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.nodes[ip]
	if !ok {
		return
	}
	if status >= 500 {
		if node.ReqLastHit.IsZero() || now.Sub(node.ReqLastHit) > overloadRecoveryTTL {
			node.ReqStreak = 0
		}
		node.ReqStreak++
		node.ReqLastHit = now
		return
	}
	node.ReqStreak = 0
}

func (m *Monitor) loop() {
	defer close(m.done)
	ticker := time.NewTicker(m.cfg.Gateway.HealthInterval)
	defer ticker.Stop()

	m.discoverAll() // non-blocking
	m.checkAll()    // immediate first health check (unchanged)

	for {
		select {
		case <-ticker.C:
			select {
			case <-m.stop:
				return
			default:
			}
			m.discoverAll()
			m.checkAll()
		case <-m.stop:
			return
		}
	}
}

func (m *Monitor) runCheck(nc config.NodeConfig) {
	defer func() {
		if r := recover(); r != nil {
			metrics.HealthCheckPanicsTotal.Inc()
			log.Error("health check panicked", "node", nc.IP, "panic", fmt.Sprint(r))
		}
	}()
	m.checkNode(nc)
}

func (m *Monitor) checkAll() {
	var wg sync.WaitGroup
	for _, nc := range m.cfg.Nodes {
		wg.Add(1)
		go func(nc config.NodeConfig) {
			defer wg.Done()
			m.runCheck(nc)
		}(nc)
	}
	wg.Wait()
}

func (m *Monitor) checkNode(nc config.NodeConfig) {
	if !m.tryStart(nc.IP) {
		return
	}
	defer m.finish(nc.IP)

	m.mu.Lock()
	node := m.nodes[nc.IP]
	m.mu.Unlock()

	// Check vLLM health
	healthURL := fmt.Sprintf("http://%s:%d/health", nc.IP, nc.VLLMPort)
	healthy := m.pingHealth(healthURL)

	// Check GPU metrics
	metricsURL := fmt.Sprintf("http://%s:%d/metrics", nc.IP, nc.MetricsPort)
	gpuMetrics := m.scrapeMetrics(metricsURL)

	m.mu.Lock()
	defer m.mu.Unlock()

	node.LastCheck = time.Now()

	if healthy {
		node.ConsecutiveFailures = 0
		node.Status = computeStatus(0, m.cfg.Gateway.HealthFailThreshold)
	} else {
		node.ConsecutiveFailures++
		node.Status = computeStatus(node.ConsecutiveFailures, m.cfg.Gateway.HealthFailThreshold)
	}

	if gpuMetrics != nil {
		node.GPUs = make([]GPUMetrics, len(gpuMetrics))
		for i, gm := range gpuMetrics {
			node.GPUs[i] = GPUMetrics{
				UUID:        gm.GPUUUID,
				MemoryUsed:  gm.MemoryUsed,
				MemoryTotal: gm.MemoryTotal,
				Utilization: gm.Utilization,
				Temperature: gm.Temperature,
			}
		}
	}
}

func (m *Monitor) tryStart(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checking[ip] {
		return false
	}
	m.checking[ip] = true
	return true
}

func (m *Monitor) finish(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.checking, ip)
}

func (m *Monitor) tryStartModels(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modelsChecking[ip] {
		return false
	}
	m.modelsChecking[ip] = true
	return true
}

func (m *Monitor) finishModels(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.modelsChecking, ip)
}

type gpuMetricsRaw struct {
	GPUUUID     string
	MemoryUsed  uint64
	MemoryTotal uint64
	Utilization uint32
	Temperature uint32
}

func (m *Monitor) pingHealth(url string) bool {
	resp, err := m.client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func extractGPUUUID(metricName string) string {
	start := strings.Index(metricName, `gpu="`)
	if start == -1 {
		return ""
	}
	start += 5
	end := strings.Index(metricName[start:], `"`)
	if end == -1 {
		return ""
	}
	return metricName[start : start+end]
}

func (m *Monitor) scrapeMetrics(url string) []gpuMetricsRaw {
	resp, err := m.client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	gpuMap := make(map[string]*gpuMetricsRaw)

	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		metricName := parts[0]
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}

		uuid := extractGPUUUID(metricName)
		if uuid == "" {
			continue
		}

		gpu, ok := gpuMap[uuid]
		if !ok {
			gpu = &gpuMetricsRaw{GPUUUID: uuid}
			gpuMap[uuid] = gpu
		}

		baseName := metricName[:strings.Index(metricName, "{")]

		switch baseName {
		case "gpu_memory_used_bytes":
			gpu.MemoryUsed = uint64(val)
		case "gpu_memory_total_bytes":
			gpu.MemoryTotal = uint64(val)
		case "gpu_utilization_percent":
			gpu.Utilization = uint32(val)
		case "gpu_temperature_celsius":
			gpu.Temperature = uint32(val)
		}
	}

	if len(gpuMap) == 0 {
		return nil
	}

	result := make([]gpuMetricsRaw, 0, len(gpuMap))
	for _, gpu := range gpuMap {
		result = append(result, *gpu)
	}
	return result
}
