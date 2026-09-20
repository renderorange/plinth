package health

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"plinth/internal/config"
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

	select {
	case <-done:
		// guarded: checkAll skipped the busy node and returned immediately
	case <-time.After(200 * time.Millisecond):
		// unguarded: checkAll is blocked on the release channel; unblock below
	}
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
		t.Errorf("ConsecutiveFailures = %d, want 1 (second admission would double-count)", got)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("health endpoint hits = %d, want 1", got)
	}
}

func newServerOnIP(t *testing.T, ip string, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	l, err := net.Listen("tcp", ip+":0")
	if err != nil {
		t.Fatalf("listen on %s: %v", ip, err)
	}
	srv.Listener = l
	srv.Start()
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

	waitForCondition(t, time.Second, "fast node updated while slow node in flight", func() bool {
		for _, st := range mon.GetNodeStates() {
			if st.IP == "127.0.0.1" && !st.LastCheck.IsZero() {
				return true
			}
		}
		return false
	})

	select {
	case <-done:
	case <-time.After(4 * time.Second):
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
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after the round drained")
	}

	if got := mon.GetNodeStates()[0].ConsecutiveFailures; got != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1 (drained round must write its result)", got)
	}
}
