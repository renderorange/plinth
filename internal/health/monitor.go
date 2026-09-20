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
)

type Monitor struct {
	cfg      *config.Config
	client   *http.Client
	nodes    map[string]*NodeState
	checking map[string]bool
	mu       sync.RWMutex
	stop     chan struct{}
	done     chan struct{}
}

func NewMonitor(cfg *config.Config) *Monitor {
	nodes := make(map[string]*NodeState)
	for _, n := range cfg.Nodes {
		nodes[n.IP] = &NodeState{
			IP:     n.IP,
			Name:   n.Name,
			Status: Healthy,
		}
	}
	return &Monitor{
		cfg: cfg,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		nodes:    nodes,
		checking: make(map[string]bool),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (m *Monitor) Start() {
	go m.loop()
}

func (m *Monitor) Stop() {
	close(m.stop)
	<-m.done
}

func (m *Monitor) GetNodeStates() []NodeState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make([]NodeState, 0, len(m.nodes))
	for _, n := range m.nodes {
		states = append(states, *n)
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
	go m.checkNode(nc)
}

func (m *Monitor) loop() {
	defer close(m.done)
	ticker := time.NewTicker(m.cfg.Gateway.HealthInterval)
	defer ticker.Stop()

	m.checkAll() // immediate first check

	for {
		select {
		case <-ticker.C:
			m.checkAll()
		case <-m.stop:
			return
		}
	}
}

func (m *Monitor) checkAll() {
	for _, nc := range m.cfg.Nodes {
		m.checkNode(nc)
	}
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
