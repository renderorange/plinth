package health

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"distributed-vram/internal/config"
)

type Monitor struct {
	cfg    *config.Config
	client *http.Client
	nodes  map[string]*NodeState
	mu     sync.RWMutex
	stop   chan struct{}
	done   chan struct{}
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
		nodes: nodes,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
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
	return states
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
		node.GPUMemoryUsed = gpuMetrics.MemoryUsed
		node.GPUMemoryTotal = gpuMetrics.MemoryTotal
		node.GPUUtilization = gpuMetrics.Utilization
		node.GPUTemperature = gpuMetrics.Temperature
	}
}

type gpuMetricsRaw struct {
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

func (m *Monitor) scrapeMetrics(url string) *gpuMetricsRaw {
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

	metrics := &gpuMetricsRaw{}
	found := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}
		switch parts[0] {
		case "gpu_memory_used_bytes":
			metrics.MemoryUsed = uint64(val)
			found = true
		case "gpu_memory_total_bytes":
			metrics.MemoryTotal = uint64(val)
			found = true
		case "gpu_utilization_percent":
			metrics.Utilization = uint32(val)
			found = true
		case "gpu_temperature_celsius":
			metrics.Temperature = uint32(val)
			found = true
		}
	}
	if !found {
		return nil
	}
	return metrics
}
