package health

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"plinth/internal/config"
	"plinth/internal/metrics"

	dto "github.com/prometheus/client_model/go"
)

func waitForCondition(t *testing.T, timeout time.Duration, desc string, check func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if check() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", desc)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestMonitorChecksNodes(t *testing.T) {
	// Fake vLLM health endpoint
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer vllm.Close()

	// Fake metrics endpoint
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("gpu_memory_used_bytes{gpu=\"GPU-abc\"} 100\ngpu_memory_total_bytes{gpu=\"GPU-abc\"} 200\ngpu_utilization_percent{gpu=\"GPU-abc\"} 50\ngpu_temperature_celsius{gpu=\"GPU-abc\"} 40\n"))
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

	waitForCondition(t, 2*time.Second, "node healthy", func() bool {
		states := mon.GetNodeStates()
		return len(states) == 1 && states[0].Status == Healthy
	})

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

	waitForCondition(t, 2*time.Second, "node dead", func() bool {
		states := mon.GetNodeStates()
		return len(states) == 1 && states[0].Status == Dead
	})

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
		w.Write([]byte("gpu_memory_used_bytes{gpu=\"GPU-test\"} 8589934592\ngpu_memory_total_bytes{gpu=\"GPU-test\"} 25769803776\ngpu_utilization_percent{gpu=\"GPU-test\"} 87\ngpu_temperature_celsius{gpu=\"GPU-test\"} 72\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	metrics := mon.scrapeMetrics(srv.URL)
	if metrics == nil {
		t.Fatal("expected metrics, got nil")
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(metrics))
	}
	m := metrics[0]
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
		w.Write([]byte("# comment line\ngpu_memory_used_bytes not_a_number\nonly_one_field\ngpu_memory_used_bytes{gpu=\"GPU-mal\"} 100\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	metrics := mon.scrapeMetrics(srv.URL)
	if metrics == nil {
		t.Fatal("expected metrics from valid line, got nil")
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(metrics))
	}
	if metrics[0].MemoryUsed != 100 {
		t.Errorf("MemoryUsed = %d, want 100 (should skip malformed lines)", metrics[0].MemoryUsed)
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
		w.Write([]byte("gpu_memory_used_bytes{gpu=\"GPU-part\"} 500\n"))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: 1, MetricsPort: extractPort(srv.URL)}},
	}
	mon := NewMonitor(cfg)

	metrics := mon.scrapeMetrics(srv.URL)
	if metrics == nil {
		t.Fatal("expected metrics with partial fields, got nil")
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(metrics))
	}
	if metrics[0].MemoryUsed != 500 {
		t.Errorf("MemoryUsed = %d, want 500", metrics[0].MemoryUsed)
	}
	if metrics[0].MemoryTotal != 0 {
		t.Errorf("MemoryTotal = %d, want 0 (not provided)", metrics[0].MemoryTotal)
	}
}

func TestMonitorGPUFieldsPopulated(t *testing.T) {
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer vllm.Close()

	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("gpu_memory_used_bytes{gpu=\"GPU-gpu\"} 1073741824\ngpu_memory_total_bytes{gpu=\"GPU-gpu\"} 8589934592\ngpu_utilization_percent{gpu=\"GPU-gpu\"} 65\ngpu_temperature_celsius{gpu=\"GPU-gpu\"} 58\n"))
	}))
	defer metrics.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      100 * time.Millisecond,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "gpu-node", VLLMPort: extractPort(vllm.URL), MetricsPort: extractPort(metrics.URL)},
		},
	}

	mon := NewMonitor(cfg)
	mon.Start()
	defer mon.Stop()

	waitForCondition(t, 2*time.Second, "GPU fields populated", func() bool {
		states := mon.GetNodeStates()
		return len(states) == 1 && len(states[0].GPUs) == 1 && states[0].GPUs[0].MemoryUsed == 1073741824
	})

	states := mon.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 node, got %d", len(states))
	}

	s := states[0]
	if len(s.GPUs) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(s.GPUs))
	}
	gpu := s.GPUs[0]
	if gpu.MemoryUsed != 1073741824 {
		t.Errorf("MemoryUsed = %d, want 1073741824", gpu.MemoryUsed)
	}
	if gpu.MemoryTotal != 8589934592 {
		t.Errorf("MemoryTotal = %d, want 8589934592", gpu.MemoryTotal)
	}
	if gpu.Utilization != 65 {
		t.Errorf("Utilization = %d, want 65", gpu.Utilization)
	}
	if gpu.Temperature != 58 {
		t.Errorf("Temperature = %d, want 58", gpu.Temperature)
	}
}

func TestMonitorResetsFailuresOnRecovery(t *testing.T) {
	failCount := 0
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK) // model discovery is not the health round
			return
		}
		failCount++
		if failCount <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer vllm.Close()

	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("gpu_memory_used_bytes{gpu=\"GPU-rec\"} 100\n"))
	}))
	defer metrics.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      50 * time.Millisecond,
			HealthFailThreshold: 5,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "recover-node", VLLMPort: extractPort(vllm.URL), MetricsPort: extractPort(metrics.URL)},
		},
	}

	mon := NewMonitor(cfg)
	mon.Start()
	defer mon.Stop()

	waitForCondition(t, 2*time.Second, "recovery to healthy", func() bool {
		states := mon.GetNodeStates()
		return len(states) == 1 && states[0].Status == Healthy && states[0].ConsecutiveFailures == 0
	})

	states := mon.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 node, got %d", len(states))
	}

	if states[0].Status != Healthy {
		t.Errorf("status = %v, want Healthy after recovery", states[0].Status)
	}
	if states[0].ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0 after recovery", states[0].ConsecutiveFailures)
	}
}

func TestNewMonitorNodesStartHealthy(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", VLLMPort: 8000, MetricsPort: 9100},
			{IP: "10.0.0.2", Name: "node-2", VLLMPort: 8000, MetricsPort: 9100},
		},
	}

	mon := NewMonitor(cfg)
	states := mon.GetNodeStates()

	if len(states) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(states))
	}
	for _, s := range states {
		if s.Status != Healthy {
			t.Errorf("node %s initial status = %v, want Healthy", s.Name, s.Status)
		}
	}
}

func TestGetNodeStatesSortedByIP(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.3", Name: "node-3", VLLMPort: 8000, MetricsPort: 9100},
			{IP: "10.0.0.1", Name: "node-1", VLLMPort: 8000, MetricsPort: 9100},
			{IP: "10.0.0.2", Name: "node-2", VLLMPort: 8000, MetricsPort: 9100},
		},
	}

	mon := NewMonitor(cfg)
	states := mon.GetNodeStates()

	if len(states) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(states))
	}
	want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	for i, s := range states {
		if s.IP != want[i] {
			t.Errorf("states[%d].IP = %s, want %s", i, s.IP, want[i])
		}
	}
}

func TestScrapeMetricsMultiGPU(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
gpu_memory_used_bytes{gpu="GPU-aaa"} 1000
gpu_memory_used_bytes{gpu="GPU-bbb"} 2000
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
gpu_memory_total_bytes{gpu="GPU-aaa"} 4000
gpu_memory_total_bytes{gpu="GPU-bbb"} 8000
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
gpu_utilization_percent{gpu="GPU-aaa"} 50
gpu_utilization_percent{gpu="GPU-bbb"} 75
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_temperature_celsius{gpu="GPU-aaa"} 60
gpu_temperature_celsius{gpu="GPU-bbb"} 70
`)
	}))
	defer ts.Close()

	m := &Monitor{client: ts.Client()}
	metrics := m.scrapeMetrics(ts.URL)
	if metrics == nil {
		t.Fatal("expected metrics, got nil")
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 GPUs, got %d", len(metrics))
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].GPUUUID < metrics[j].GPUUUID
	})
	if metrics[0].GPUUUID != "GPU-aaa" {
		t.Errorf("expected GPU-aaa, got %s", metrics[0].GPUUUID)
	}
	if metrics[0].MemoryUsed != 1000 {
		t.Errorf("expected 1000, got %d", metrics[0].MemoryUsed)
	}
	if metrics[1].GPUUUID != "GPU-bbb" {
		t.Errorf("expected GPU-bbb, got %s", metrics[1].GPUUUID)
	}
	if metrics[1].MemoryUsed != 2000 {
		t.Errorf("expected 2000, got %d", metrics[1].MemoryUsed)
	}
}

func TestScrapeMetricsSingleGPU(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
gpu_memory_used_bytes{gpu="GPU-ccc"} 500
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
gpu_memory_total_bytes{gpu="GPU-ccc"} 1000
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
gpu_utilization_percent{gpu="GPU-ccc"} 25
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_temperature_celsius{gpu="GPU-ccc"} 45
`)
	}))
	defer ts.Close()

	m := &Monitor{client: ts.Client()}
	metrics := m.scrapeMetrics(ts.URL)
	if metrics == nil {
		t.Fatal("expected metrics, got nil")
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(metrics))
	}
	if metrics[0].GPUUUID != "GPU-ccc" {
		t.Errorf("expected GPU-ccc, got %s", metrics[0].GPUUUID)
	}
}

func TestScrapeMetricsNoGPU(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
`)
	}))
	defer ts.Close()

	m := &Monitor{client: ts.Client()}
	metrics := m.scrapeMetrics(ts.URL)
	if metrics != nil {
		t.Errorf("expected nil, got %v", metrics)
	}
}

func TestExtractGPUUUID(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{`gpu_memory_used_bytes{gpu="GPU-aaa"} 1000`, "GPU-aaa"},
		{`gpu_memory_total_bytes{gpu="GPU-bbb"} 2000`, "GPU-bbb"},
		{`gpu_memory_used_bytes 1000`, ""},
		{`gpu_memory_used_bytes{gpu=""} 1000`, ""},
	}
	for _, tt := range tests {
		got := extractGPUUUID(tt.name)
		if got != tt.want {
			t.Errorf("extractGPUUUID(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMonitorRecheckUpdatesState(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			// closed port → health check fails
			{IP: "127.0.0.1", Name: "node-1", VLLMPort: 1, MetricsPort: 1},
		},
		Models: config.ModelsConfig{
			Available: []config.ModelConfig{{Name: "test/model", PipelineStages: 1}},
		},
	}
	mon := NewMonitor(cfg)
	states := mon.GetNodeStates()
	if len(states) != 1 || states[0].Status != Healthy {
		t.Fatalf("initial state = %+v, want single healthy node", states)
	}

	mon.Recheck("127.0.0.1")

	deadline := time.After(2 * time.Second)
	for {
		states = mon.GetNodeStates()
		if states[0].ConsecutiveFailures >= 1 || states[0].Status == Degraded {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("Recheck did not update node state: %+v", states)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestMonitorRecheckUnknownIPNoPanic(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1", VLLMPort: 8000, MetricsPort: 9100},
		},
		Models: config.ModelsConfig{
			Available: []config.ModelConfig{{Name: "test/model", PipelineStages: 1}},
		},
	}
	mon := NewMonitor(cfg)
	mon.Recheck("10.0.0.99") // must not panic, must not create state
	if len(mon.GetNodeStates()) != 1 {
		t.Error("Recheck(unknown) mutated node set")
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

func TestCheckAllSkipsNodeWithInFlightRecheck(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer blocked.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "n", VLLMPort: extractPort(blocked.URL), MetricsPort: 1},
		},
	}
	mon := NewMonitor(cfg)

	mon.Recheck("127.0.0.1")
	waitForCondition(t, time.Second, "recheck probe in flight", func() bool { return hits.Load() == 1 })

	done := make(chan struct{})
	go func() { mon.checkAll(); close(done) }()

	guardedFastPath := false
	select {
	case <-done:
		guardedFastPath = true
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("checkAll did not return")
	}

	if !guardedFastPath {
		t.Fatal("checkAll blocked on the in-flight node; guard fast path was not taken")
	}

	waitForCondition(t, time.Second, "failure recorded", func() bool {
		return mon.GetNodeStates()[0].ConsecutiveFailures >= 1
	})
	time.Sleep(50 * time.Millisecond)
	if got := mon.GetNodeStates()[0].ConsecutiveFailures; got != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1 (second admission would double-count)", got)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("health endpoint hits = %d, want 1", got)
	}
}

func newServerOnIP(t *testing.T, ip string, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	defaultListener := srv.Listener
	l, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Fatalf("listen on %s: %v", ip, err)
	}
	srv.Listener = l
	srv.Start()
	defaultListener.Close()
	return srv
}

func TestCheckAllDoesNotStallOnSlowNode(t *testing.T) {
	slow := newServerOnIP(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fast.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.2", Name: "slow", VLLMPort: extractPort(slow.URL), MetricsPort: 1}, // slow node first to make sequential checks stall
			{IP: "127.0.0.1", Name: "fast", VLLMPort: extractPort(fast.URL), MetricsPort: 1},
		},
	}
	mon := NewMonitor(cfg)

	done := make(chan struct{})
	go func() { mon.checkAll(); close(done) }()

	waitForCondition(t, 2*time.Second, "fast node updated while slow node in flight", func() bool {
		for _, st := range mon.GetNodeStates() {
			if st.IP == "127.0.0.1" && !st.LastCheck.IsZero() {
				return true
			}
		}
		return false
	})

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("checkAll did not return after slow node finished")
	}
	for _, st := range mon.GetNodeStates() {
		if st.IP == "127.0.0.2" && st.LastCheck.IsZero() {
			t.Error("slow node was never checked")
		}
	}
}

func TestRecheckNoOpWhenCheckInFlight(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer blocked.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "n", VLLMPort: extractPort(blocked.URL), MetricsPort: 1},
		},
	}
	mon := NewMonitor(cfg)

	done := make(chan struct{})
	go func() { mon.checkAll(); close(done) }()
	waitForCondition(t, time.Second, "cycle probe in flight", func() bool { return hits.Load() == 1 })

	mon.Recheck("127.0.0.1") // must not spawn a second probe

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("checkAll did not return")
	}

	waitForCondition(t, time.Second, "failure recorded", func() bool {
		return mon.GetNodeStates()[0].ConsecutiveFailures >= 1
	})
	time.Sleep(50 * time.Millisecond)
	if got := mon.GetNodeStates()[0].ConsecutiveFailures; got != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1 (admitted Recheck would double-count)", got)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("health endpoint hits = %d, want 1", got)
	}
}

type panicOnceTransport struct {
	inner    http.RoundTripper
	panicked *atomic.Bool
}

func (p *panicOnceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.panicked.CompareAndSwap(false, true) {
		panic("injected probe panic")
	}
	return p.inner.RoundTrip(req)
}

type alwaysPanicTransport struct{}

func (p *alwaysPanicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	panic("injected probe panic")
}

func TestPanicIncrementsPanicsCounter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n", VLLMPort: extractPort(srv.URL), MetricsPort: 1}},
	}
	mon := NewMonitor(cfg)
	mon.client.Transport = &alwaysPanicTransport{}

	before := &dto.Metric{}
	if err := metrics.HealthCheckPanicsTotal.Write(before); err != nil {
		t.Fatalf("HealthCheckPanicsTotal not registered: %v", err)
	}
	beforeVal := before.GetCounter().GetValue()

	mon.checkAll()
	mon.checkAll()

	after := &dto.Metric{}
	if err := metrics.HealthCheckPanicsTotal.Write(after); err != nil {
		t.Fatalf("HealthCheckPanicsTotal not registered: %v", err)
	}
	if got := after.GetCounter().GetValue() - beforeVal; got != 2 {
		t.Errorf("HealthCheckPanicsTotal delta = %f, want 2", got)
	}
	if got := mon.GetNodeStates()[0].LastCheck; !got.IsZero() {
		t.Errorf("LastCheck = %v, want zero", got)
	}
}

func TestCheckGuardClearedOnPanic(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "n", VLLMPort: extractPort(srv.URL), MetricsPort: 1},
		},
	}
	mon := NewMonitor(cfg)
	var panicked atomic.Bool
	mon.client.Transport = &panicOnceTransport{inner: http.DefaultTransport, panicked: &panicked}

	mon.checkAll() // first round: probe panics inside; runCheck must recover
	mon.checkAll() // second round: guard flag was cleared, probe runs again

	waitForCondition(t, time.Second, "node checked after panic", func() bool {
		return !mon.GetNodeStates()[0].LastCheck.IsZero()
	})
	if !panicked.Load() {
		t.Error("panic transport was never exercised")
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("health endpoint hits = %d, want 1 (wedged guard would skip the second round)", got)
	}
}

func TestStopDrainsInFlightRound(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			return // model discovery is not the health round
		}
		hits.Add(1)
		<-release
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer blocked.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "n", VLLMPort: extractPort(blocked.URL), MetricsPort: 1},
		},
	}
	mon := NewMonitor(cfg)
	mon.Start()
	waitForCondition(t, time.Second, "probe in flight", func() bool { return hits.Load() == 1 })

	stopped := make(chan struct{})
	go func() { mon.Stop(); close(stopped) }()

	select {
	case <-stopped:
		t.Fatal("Stop returned while a round was in flight")
	case <-time.After(500 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the round drained")
	}

	if got := mon.GetNodeStates()[0].ConsecutiveFailures; got != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1 (drained round must write its result)", got)
	}
}

func TestNewMonitorWithStateCarriesOverStatus(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
			{IP: "10.0.0.2", Name: "node-2"},
		},
	}

	prev := []NodeState{
		{IP: "10.0.0.1", Status: Dead, ConsecutiveFailures: 5},
		{IP: "10.0.0.9", Status: Degraded, ConsecutiveFailures: 1},
		{IP: "10.0.0.2", Status: Degraded, ConsecutiveFailures: 2},
	}

	mon := NewMonitorWithState(cfg, prev)

	states := mon.GetNodeStates()
	if len(states) != 2 {
		t.Fatalf("states = %d, want 2 (only nodes from the newest config)", len(states))
	}

	byIP := make(map[string]NodeState, len(states))
	for _, s := range states {
		byIP[s.IP] = s
	}

	if s := byIP["10.0.0.1"]; s.Status != Dead || s.ConsecutiveFailures != 5 {
		t.Errorf("10.0.0.1 = status %v failures %d, want Dead with 5 carried over", s.Status, s.ConsecutiveFailures)
	}
	if s := byIP["10.0.0.2"]; s.Status != Degraded || s.ConsecutiveFailures != 2 {
		t.Errorf("10.0.0.2 = status %v failures %d, want Degraded with 2 carried over", s.Status, s.ConsecutiveFailures)
	}
	if _, ok := byIP["10.0.0.9"]; ok {
		t.Error("removed node 10.0.0.9 must not appear in the new monitor")
	}
}

func TestNewMonitorWithStateFreshNodesStartHealthy(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "fresh-node"},
		},
	}

	prev := []NodeState{
		{IP: "10.0.0.9", Status: Dead, ConsecutiveFailures: 4},
	}

	mon := NewMonitorWithState(cfg, prev)

	states := mon.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("states = %d, want 1", len(states))
	}
	if states[0].Status != Healthy || states[0].ConsecutiveFailures != 0 {
		t.Errorf("fresh node = status %v failures %d, want Healthy with 0", states[0].Status, states[0].ConsecutiveFailures)
	}
}

func TestNewMonitorWithStateEmptyPrevAllHealthy(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:      time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
	}

	mon := NewMonitorWithState(cfg, nil)

	states := mon.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("states = %d, want 1", len(states))
	}
	if states[0].Status != Healthy || states[0].ConsecutiveFailures != 0 {
		t.Errorf("node = status %v failures %d, want Healthy with 0", states[0].Status, states[0].ConsecutiveFailures)
	}
}

func TestStopDoesNotStartNewRound(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			return // model discovery is not the health round
		}
		hits.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: 20 * time.Millisecond, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "n", VLLMPort: extractPort(srv.URL), MetricsPort: 1},
		},
	}

	for i := 0; i < 12; i++ {
		mon := NewMonitor(cfg)
		before := hits.Load()
		mon.Start()
		waitForCondition(t, time.Second, "round in flight", func() bool {
			return hits.Load() == before+1
		})
		mon.Stop()
		if got := hits.Load(); got != before+1 {
			t.Errorf("iteration %d: health endpoint hits = %d, want %d (no new round after Stop)", i, got, before+1)
		}
	}
}

func TestStopIsIdempotent(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "test-node"},
		},
	}
	mon := NewMonitor(cfg)
	mon.Start()
	mon.Stop()
	mon.Stop()
}

func TestNewMonitorNodesStartUntried(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
	}
	m := NewMonitor(cfg)
	states := m.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("states = %d, want 1", len(states))
	}
	if states[0].Models.State != ModelsUntried {
		t.Errorf("State = %v, want ModelsUntried", states[0].Models.State)
	}
	if !states[0].Models.Allows("anything") {
		t.Error("untried node must allow all models")
	}
}

func TestGetNodeStatesDeepCopiesModelNames(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
	}
	m := NewMonitor(cfg)
	m.mu.Lock()
	m.nodes["10.0.0.1"].Models = ModelDiscovery{
		State: ModelsKnown,
		Names: []string{"a", "b"},
	}
	m.mu.Unlock()

	states := m.GetNodeStates()
	if len(states) != 1 || len(states[0].Models.Names) != 2 {
		t.Fatalf("states = %+v", states)
	}
	states[0].Models.Names[0] = "MUTATED"

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.nodes["10.0.0.1"].Models.Names[0] != "a" {
		t.Errorf("source Names[0] = %q, want \"a\" (deep copy failed)", m.nodes["10.0.0.1"].Models.Names[0])
	}
}

func TestGetNodeStatesDeepCopiesGPUs(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
	}
	m := NewMonitor(cfg)
	m.mu.Lock()
	m.nodes["10.0.0.1"].GPUs = []GPUMetrics{
		{UUID: "GPU-abc", MemoryUsed: 100, MemoryTotal: 200, Utilization: 50, Temperature: 40},
	}
	m.mu.Unlock()

	states := m.GetNodeStates()
	if len(states) != 1 || len(states[0].GPUs) != 1 {
		t.Fatalf("states = %+v", states)
	}
	states[0].GPUs[0].UUID = "MUTATED"

	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.nodes["10.0.0.1"].GPUs[0].UUID != "GPU-abc" {
		t.Errorf("source GPUs[0].UUID = %q, want \"GPU-abc\" (deep copy failed)", m.nodes["10.0.0.1"].GPUs[0].UUID)
	}
}

func TestNodeStateCloneIsolatesAllSliceFields(t *testing.T) {
	src := NodeState{
		IP:                  "10.0.0.1",
		Name:                "n1",
		Status:              Degraded,
		LastCheck:           time.Now(),
		ConsecutiveFailures: 2,
		GPUs: []GPUMetrics{
			{UUID: "GPU-abc", MemoryUsed: 100, MemoryTotal: 200, Utilization: 50, Temperature: 40},
			{UUID: "GPU-def", MemoryUsed: 110, MemoryTotal: 200, Utilization: 60, Temperature: 41},
		},
		Models: ModelDiscovery{
			State:     ModelsKnown,
			Names:     []string{"a", "b"},
			FetchedAt: time.Now(),
			Fails:     1,
			Empties:   1,
			LastError: "old",
		},
	}
	clone := src.Clone()

	checkSlice := func(name string, a, b reflect.Value) {
		t.Helper()
		if a.Len() != b.Len() {
			t.Fatalf("%s: clone len = %d, want %d", name, b.Len(), a.Len())
		}
		if a.Pointer() == b.Pointer() {
			t.Fatalf("%s: clone shares a backing array with the source", name)
		}
		elemType := a.Type().Elem()
		orig := a.Index(0).Interface()

		b.Index(0).Set(reflect.Zero(elemType))
		if !reflect.DeepEqual(a.Index(0).Interface(), orig) {
			t.Errorf("%s: mutating the clone element changed the source", name)
		}
		b.Index(0).Set(reflect.ValueOf(orig))

		a.Index(0).Set(reflect.Zero(elemType))
		if !reflect.DeepEqual(b.Index(0).Interface(), orig) {
			t.Errorf("%s: mutating the source element changed the clone", name)
		}
		a.Index(0).Set(reflect.ValueOf(orig))
	}

	// Fixture completeness: every slice field must be populated so a newly
	// added field fails this test until the fixture covers it. The walks pick
	// up new fields automatically.
	walk := func(prefix string, s, c reflect.Value) {
		t.Helper()
		for i := 0; i < s.NumField(); i++ {
			f := s.Type().Field(i)
			if f.Type.Kind() != reflect.Slice {
				continue
			}
			if s.Field(i).Len() == 0 {
				t.Fatalf("fixture slice %s%s is empty; populate it so Clone stays covered", prefix, f.Name)
			}
			checkSlice(prefix+f.Name, s.Field(i), c.Field(i))
		}
	}
	walk("", reflect.ValueOf(src), reflect.ValueOf(clone))
	walk("Models.", reflect.ValueOf(src.Models), reflect.ValueOf(clone.Models))
}

func TestNewMonitorWithStateCarriesModels(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
	}
	prev := []NodeState{{
		IP:     "10.0.0.1",
		Name:   "n1",
		Status: Healthy,
		Models: ModelDiscovery{
			State:     ModelsDegraded,
			Names:     []string{"a"},
			FetchedAt: time.Now(),
			Fails:     2,
			Empties:   1,
			LastError: "old",
		},
	}}
	m := NewMonitorWithState(cfg, prev)
	states := m.GetNodeStates()
	if len(states) != 1 {
		t.Fatalf("states = %d, want 1", len(states))
	}
	md := states[0].Models
	if md.State != ModelsDegraded || md.Fails != 2 || md.Empties != 1 || md.LastError != "old" {
		t.Errorf("Models = %+v, want degraded/fails=2/empties=1/LastError=old", md)
	}
	if len(md.Names) != 1 || md.Names[0] != "a" {
		t.Errorf("Names = %v, want [a]", md.Names)
	}
	if md.FetchedAt.IsZero() {
		t.Error("FetchedAt not carried")
	}

	// Deep copy on carryover: mutating the new node must not touch prev.
	states[0].Models.Names[0] = "MUTATED"
	if prev[0].Models.Names[0] != "a" {
		t.Errorf("prev Names[0] = %q, want \"a\"", prev[0].Models.Names[0])
	}

	// The snapshot mutation above cannot see carryover aliasing: GetNodeStates
	// already deep-copies, so that assert passes even if the monitor's live
	// Names share prev's backing array. Mutate the live node for real teeth.
	m.mu.Lock()
	m.nodes["10.0.0.1"].Models.Names[0] = "MUTATED-LIVE"
	m.mu.Unlock()
	if prev[0].Models.Names[0] != "a" {
		t.Errorf("prev Names[0] = %q, want \"a\" after live mutation (carryover deep copy failed)", prev[0].Models.Names[0])
	}
}

func TestNewMonitorWithStateFreshNodeStaysUntried(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Second, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.2", Name: "n2", VLLMPort: 8000}},
	}
	prev := []NodeState{{IP: "10.0.0.1", Models: ModelDiscovery{State: ModelsKnown, Names: []string{"a"}}}}
	m := NewMonitorWithState(cfg, prev)
	states := m.GetNodeStates()
	if states[0].Models.State != ModelsUntried {
		t.Errorf("fresh node State = %v, want ModelsUntried", states[0].Models.State)
	}
}

func TestTryStartModelsIndependentOfChecking(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
	}
	m := NewMonitor(cfg)
	if !m.tryStart("10.0.0.1") {
		t.Fatal("tryStart failed")
	}
	if !m.tryStartModels("10.0.0.1") {
		t.Fatal("tryStartModels must not be blocked by health checking")
	}
	if m.tryStartModels("10.0.0.1") {
		t.Fatal("second tryStartModels must fail")
	}
	m.finishModels("10.0.0.1")
	if !m.tryStartModels("10.0.0.1") {
		t.Fatal("tryStartModels after finishModels")
	}
}
