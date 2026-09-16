package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"distributed-vram/internal/config"
)

func TestMonitorChecksNodes(t *testing.T) {
	// Fake vLLM health endpoint
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer vllm.Close()

	// Fake metrics endpoint
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("gpu_memory_used_bytes 100\ngpu_memory_total_bytes 200\ngpu_utilization_percent 50\ngpu_temperature_celsius 40\n"))
	}))
	defer metrics.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      100 * time.Millisecond,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "test-node", VLLMPort: extractPort(vllm.URL), MetricsPort: extractPort(metrics.URL)},
		},
	}

	mon := NewMonitor(cfg)
	mon.Start()
	defer mon.Stop()

	// Wait for at least one check cycle
	time.Sleep(200 * time.Millisecond)

	states := mon.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 node state, got %d", len(states))
	}
	if states[0].Status != Healthy {
		t.Errorf("node status = %v, want Healthy", states[0].Status)
	}
}

func TestMonitorDetectsDeadNode(t *testing.T) {
	// Start a server then immediately close it to get a port that refuses connections
	tmp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedPort := extractPort(tmp.URL)
	tmp.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      50 * time.Millisecond,
			HealthFailThreshold: 2,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "dead-node", VLLMPort: closedPort, MetricsPort: closedPort + 1},
		},
	}

	mon := NewMonitor(cfg)
	mon.Start()
	defer mon.Stop()

	// Wait for enough failures
	time.Sleep(300 * time.Millisecond)

	states := mon.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 node state, got %d", len(states))
	}
	if states[0].Status != Dead {
		t.Errorf("node status = %v, want Dead", states[0].Status)
	}
}

func TestScrapeMetricsAllFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("gpu_memory_used_bytes 8589934592\ngpu_memory_total_bytes 25769803776\ngpu_utilization_percent 87\ngpu_temperature_celsius 72\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	m := mon.scrapeMetrics(srv.URL)
	if m == nil {
		t.Fatal("expected metrics, got nil")
	}
	if m.MemoryUsed != 8589934592 {
		t.Errorf("MemoryUsed = %d, want 8589934592", m.MemoryUsed)
	}
	if m.MemoryTotal != 25769803776 {
		t.Errorf("MemoryTotal = %d, want 25769803776", m.MemoryTotal)
	}
	if m.Utilization != 87 {
		t.Errorf("Utilization = %d, want 87", m.Utilization)
	}
	if m.Temperature != 72 {
		t.Errorf("Temperature = %d, want 72", m.Temperature)
	}
}

func TestScrapeMetricsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(""))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	m := mon.scrapeMetrics(srv.URL)
	if m != nil {
		t.Errorf("expected nil for empty body, got %+v", m)
	}
}

func TestScrapeMetricsMalformedLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("# comment line\ngpu_memory_used_bytes not_a_number\nonly_one_field\ngpu_memory_used_bytes 100\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	m := mon.scrapeMetrics(srv.URL)
	if m == nil {
		t.Fatal("expected metrics from valid line, got nil")
	}
	if m.MemoryUsed != 100 {
		t.Errorf("MemoryUsed = %d, want 100 (should skip malformed lines)", m.MemoryUsed)
	}
}

func TestScrapeMetricsOnlyComments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("# HELP gpu_memory_used Bytes used\n# TYPE gpu_memory_used gauge\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	m := mon.scrapeMetrics(srv.URL)
	if m != nil {
		t.Errorf("expected nil for comments-only body, got %+v", m)
	}
}

func TestScrapeMetricsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	m := mon.scrapeMetrics(srv.URL)
	if m != nil {
		t.Errorf("expected nil for 500, got %+v", m)
	}
}

func TestScrapeMetricsPartialFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("gpu_memory_used_bytes 500\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	m := mon.scrapeMetrics(srv.URL)
	if m == nil {
		t.Fatal("expected metrics with partial fields, got nil")
	}
	if m.MemoryUsed != 500 {
		t.Errorf("MemoryUsed = %d, want 500", m.MemoryUsed)
	}
	if m.MemoryTotal != 0 {
		t.Errorf("MemoryTotal = %d, want 0 (not provided)", m.MemoryTotal)
	}
}

func extractPort(url string) int {
	// Extract port from httptest.Server URL like "http://127.0.0.1:PORT"
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == ':' {
			port := 0
			for _, c := range url[i+1:] {
				if c >= '0' && c <= '9' {
					port = port*10 + int(c-'0')
				}
			}
			return port
		}
	}
	return 0
}
