package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
	"plinth/internal/metrics"
)

func newTestHandler() *Handler {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Nodes:   []config.NodeConfig{},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	return NewHandler(cfg, mon, bal)
}

func TestHealthEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestModelsEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatal("response missing data field")
	}
	if len(data) != 1 {
		t.Errorf("models count = %d, want 1", len(data))
	}
}

func TestChatCompletionsNoNodes(t *testing.T) {
	h := newTestHandler()
	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Body = io.NopCloser(strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("status = %d, want 503 (no healthy nodes)", w.Code)
	}
}

func TestCompletionsNoNodes(t *testing.T) {
	h := newTestHandler()
	body := `{"model":"test/model","prompt":"hello"}`
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestChatCompletionsInvalidJSON(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 (invalid JSON)", w.Code)
	}
}

func TestCompletionsInvalidJSON(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 (invalid JSON)", w.Code)
	}
}

func TestChatCompletionsBodyTooLarge(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{MaxRequestBodyBytes: 16},
		Nodes:   []config.NodeConfig{},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	h := NewHandler(cfg, health.NewMonitor(cfg), balancer.New())

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d (body too large)", w.Code, http.StatusRequestEntityTooLarge)
	}
}

// TestChatCompletionsBodyWithinConfiguredLimit confirms the request body cap is
// read from config rather than a hardcoded package default.
func TestChatCompletionsBodyWithinConfiguredLimit(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{MaxRequestBodyBytes: 1 << 20},
		Nodes:   []config.NodeConfig{},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	h := NewHandler(cfg, health.NewMonitor(cfg), balancer.New())

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want not 413: body is well under the configured limit", w.Code)
	}
}

func TestHealthEndpointJSONStructure(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["status"] != "error" {
		t.Errorf("status = %v, want error (no nodes)", resp["status"])
	}
	if _, ok := resp["nodes"]; !ok {
		t.Error("response missing nodes field")
	}
	if _, ok := resp["healthy"]; !ok {
		t.Error("response missing healthy field")
	}

	// A fresh node (never served a request) must expose req_streak as 0.
	h2, _ := newTestHandlerWithBackend("http://127.0.0.1:8000")
	req2 := httptest.NewRequest("GET", "/health", nil)
	w2 := httptest.NewRecorder()
	h2.ServeHTTP(w2, req2)
	var resp2 map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	details, ok := resp2["details"].([]interface{})
	if !ok || len(details) == 0 {
		t.Fatal("response missing details array")
	}
	entry, ok := details[0].(map[string]interface{})
	if !ok {
		t.Fatal("details[0] is not an object")
	}
	if _, ok := entry["req_streak"]; !ok {
		t.Error("details entry missing req_streak field")
	}
	if entry["req_streak"] != float64(0) {
		t.Errorf("req_streak = %v, want 0 for a fresh node", entry["req_streak"])
	}
}

func TestModelsEndpointStructure(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["object"] != "list" {
		t.Errorf("object = %v, want list", resp["object"])
	}
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) == 0 {
		t.Fatal("missing or empty data array")
	}
	entry, ok := data[0].(map[string]interface{})
	if !ok {
		t.Fatal("data[0] is not an object")
	}
	if entry["id"] != "test/model" {
		t.Errorf("id = %v, want test/model", entry["id"])
	}
	if entry["object"] != "model" {
		t.Errorf("object = %v, want model", entry["object"])
	}
	if entry["owned_by"] != "cluster" {
		t.Errorf("owned_by = %v, want cluster", entry["owned_by"])
	}
}

func newTestHandlerWithBackend(backendURL string) (*Handler, *health.Monitor) {
	u, _ := url.Parse(backendURL)
	port, _ := strconv.Atoi(u.Port())
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{
			HealthInterval:      100 * time.Millisecond,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "node-1", VLLMPort: port, MetricsPort: port},
		},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	return NewHandler(cfg, mon, bal), mon
}

func newTestHandlerWithPortLimit(port int, limit int64) (*Handler, *health.Monitor) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{
			HealthInterval:           100 * time.Millisecond,
			HealthFailThreshold:      3,
			MaxBufferedResponseBytes: limit,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "node-1", VLLMPort: port, MetricsPort: port},
		},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	return NewHandler(cfg, mon, bal), mon
}

func TestHealthEndpointWithHealthyNodes(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()

	waitForHealthy(t, mon)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if resp["healthy"].(float64) != 1 {
		t.Errorf("healthy = %v, want 1", resp["healthy"])
	}
}

func waitForHealthy(t *testing.T, mon *health.Monitor) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		states := mon.GetNodeStates()
		if len(states) == 1 && states[0].Status == health.Healthy {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for node to become healthy")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestProxyToVLLMHappyPath(t *testing.T) {
	var receivedBody atomic.Value
	var receivedPath atomic.Value

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody.Store(string(body))
		receivedPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"text":"hello"}]}`)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()

	waitForHealthy(t, mon)

	payload := `{"model":"test/model","prompt":"say hi"}`
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if p, ok := receivedPath.Load().(string); !ok || p != "/v1/completions" {
		t.Errorf("backend path = %q, want /v1/completions", receivedPath.Load())
	}
	if b, ok := receivedBody.Load().(string); !ok || b != payload {
		t.Errorf("body = %q, want %q", receivedBody.Load(), payload)
	}
}

func TestProxyToVLLMChatCompletions(t *testing.T) {
	var receivedPath atomic.Value

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		receivedPath.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()

	waitForHealthy(t, mon)

	payload := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if p, ok := receivedPath.Load().(string); !ok || p != "/v1/chat/completions" {
		t.Errorf("backend path = %q, want /v1/chat/completions", receivedPath.Load())
	}
}

func TestProxyToVLLMRecordsMetrics(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()

	waitForHealthy(t, mon)

	attemptsBefore := &dto.Metric{}
	metrics.ProxyAttemptsTotal.WithLabelValues("test/model", "200").Write(attemptsBefore)

	payload := `{"model":"test/model","prompt":"hi"}`
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}

	attemptsAfter := &dto.Metric{}
	metrics.ProxyAttemptsTotal.WithLabelValues("test/model", "200").Write(attemptsAfter)
	got := attemptsAfter.GetCounter().GetValue() - attemptsBefore.GetCounter().GetValue()
	if got != 1 {
		t.Errorf("ProxyAttemptsTotal delta = %f, want 1 (happy path should record exactly one attempt)", got)
	}
}

func TestModelsEndpointDataValidation(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	data, ok := resp["data"].([]interface{})
	if !ok || len(data) == 0 {
		t.Fatal("missing or empty data array")
	}

	for i, entry := range data {
		m, ok := entry.(map[string]interface{})
		if !ok {
			t.Errorf("data[%d] is not an object", i)
			continue
		}
		if _, ok := m["id"]; !ok {
			t.Errorf("data[%d] missing id field", i)
		}
		if _, ok := m["object"]; !ok {
			t.Errorf("data[%d] missing object field", i)
		}
		if _, ok := m["owned_by"]; !ok {
			t.Errorf("data[%d] missing owned_by field", i)
		}
		if m["object"] != "model" {
			t.Errorf("data[%d].object = %v, want model", i, m["object"])
		}
		if m["owned_by"] != "cluster" {
			t.Errorf("data[%d].owned_by = %v, want cluster", i, m["owned_by"])
		}
	}
}

func TestFilterByRingRoutesToRing(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-a"},
			{IP: "10.0.0.3", Name: "node-3", Ring: "ring-a"},
		},
		Models: config.ModelsConfig{
			Default: "small/model",
			Available: []config.ModelConfig{
				{Name: "big/model", PipelineStages: 2, Ring: "ring-a"},
				{Name: "small/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	h := NewHandler(cfg, mon, bal)

	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
		{IP: "10.0.0.3", Name: "node-3", Status: health.Healthy},
	}

	filtered := h.filterByRing(states, "big/model")
	if len(filtered) != 2 {
		t.Fatalf("filterByRing(big/model) = %d nodes, want 2", len(filtered))
	}
	for _, s := range filtered {
		if s.IP == "10.0.0.1" {
			t.Error("filterByRing(big/model) should not include ungrouped node")
		}
	}
}

func TestFilterByRingExcludesRingNodes(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-a"},
		},
		Models: config.ModelsConfig{
			Default: "small/model",
			Available: []config.ModelConfig{
				{Name: "big/model", PipelineStages: 2, Ring: "ring-a"},
				{Name: "small/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	h := NewHandler(cfg, mon, bal)

	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	filtered := h.filterByRing(states, "small/model")
	if len(filtered) != 1 {
		t.Fatalf("filterByRing(small/model) = %d nodes, want 1", len(filtered))
	}
	if filtered[0].IP != "10.0.0.1" {
		t.Errorf("filterByRing(small/model) selected %s, want 10.0.0.1", filtered[0].IP)
	}
}

func TestFilterByRingNoRingModelNoGroups(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
			{IP: "10.0.0.2", Name: "node-2"},
		},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	h := NewHandler(cfg, mon, bal)

	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	filtered := h.filterByRing(states, "test/model")
	if len(filtered) != 2 {
		t.Fatalf("filterByRing(test/model) = %d nodes, want 2", len(filtered))
	}
}

func TestFilterByRingUnknownModelWithRings(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-a"},
		},
		Models: config.ModelsConfig{
			Default: "small/model",
			Available: []config.ModelConfig{
				{Name: "small/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	bal := balancer.New()
	h := NewHandler(cfg, mon, bal)

	states := []health.NodeState{
		{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy},
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}

	filtered := h.filterByRing(states, "unknown/model")
	if len(filtered) != 1 {
		t.Fatalf("filterByRing(unknown/model) = %d nodes, want 1", len(filtered))
	}
	if filtered[0].IP != "10.0.0.1" {
		t.Errorf("filterByRing(unknown/model) selected %s, want 10.0.0.1", filtered[0].IP)
	}
}

func TestProxyToVLLMDefaultModelRoutesToRing(t *testing.T) {
	var receivedBody atomic.Value

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"choices":[{"text":"hello"}]}`)
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	port, _ := strconv.Atoi(u.Port())
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{
			HealthInterval:      100 * time.Millisecond,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "ring-node", VLLMPort: port, MetricsPort: port, Ring: "ring-a"},
		},
		Models: config.ModelsConfig{
			Default: "big/model",
			Available: []config.ModelConfig{
				{Name: "big/model", Ring: "ring-a"},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())
	mon.Start()
	defer mon.Stop()

	waitForHealthy(t, mon)

	payload := `{"prompt":"hello"}`
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (default ring model must route to ring node)", w.Code)
	}
	body, ok := receivedBody.Load().(string)
	if !ok {
		t.Fatal("backend did not receive a body")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("backend received invalid JSON: %v (body %q)", err, body)
	}
	if got["model"] != "big/model" {
		t.Errorf("backend body model = %v, want big/model", got["model"])
	}
}

func TestProxyToVLLMMissingModelNoDefaultReturns400(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{
			HealthInterval:      3 * time.Second,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Models: config.ModelsConfig{
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"prompt":"hi"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestOfflineSet(t *testing.T) {
	o := newOfflineSet()
	if o.skip("10.0.0.1") {
		t.Error("fresh set should not skip")
	}
	if !o.mark("10.0.0.1") {
		t.Error("first mark should be a transition")
	}
	if o.mark("10.0.0.1") {
		t.Error("second mark within TTL should not be a transition")
	}
	if !o.skip("10.0.0.1") {
		t.Error("marked IP should be skipped")
	}
	o.clear("10.0.0.1")
	if o.skip("10.0.0.1") {
		t.Error("cleared IP should not be skipped")
	}
	// expiry: backdate the entry
	o.mark("10.0.0.2")
	o.mu.Lock()
	o.until["10.0.0.2"] = time.Now().Add(-time.Minute)
	o.mu.Unlock()
	if o.skip("10.0.0.2") {
		t.Error("expired entry should not be skipped")
	}
	o.mu.Lock()
	_, stillThere := o.until["10.0.0.2"]
	o.mu.Unlock()
	if stillThere {
		t.Error("expired entry should be pruned")
	}
}

func newServerOn(t *testing.T, ip string, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Fatalf("listen on %s: %v", ip, err)
	}
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	return srv
}

func handlerWithNodes(t *testing.T, nodes []config.NodeConfig) *Handler {
	t.Helper()
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{
			HealthInterval:      100 * time.Millisecond,
			HealthFailThreshold: 3,
		},
		Nodes: nodes,
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg) // NOT started — see constraints
	return NewHandler(cfg, mon, balancer.New())
}

func TestProxyToVLLMRetriesOnConnectionFailure(t *testing.T) {
	var hits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"text":"retried"}]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	u, _ := url.Parse(good.URL)
	goodPort, _ := strconv.Atoi(u.Port())

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "dead", VLLMPort: 1, MetricsPort: 1},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (retried on good node)", w.Code)
	}
	if hits.Load() == 0 {
		t.Fatal("good node never received the request")
	}
}

func TestProxyToVLLMAllNodesDeadReturns503(t *testing.T) {
	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "dead-1", VLLMPort: 1, MetricsPort: 1},
		{IP: "127.0.0.2", Name: "dead-2", VLLMPort: 2, MetricsPort: 2},
	})
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestProxyToVLLMNoRetryOnHTTPError(t *testing.T) {
	var goodHits atomic.Int64
	bad := newServerOn(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"internal"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer bad.Close()
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			goodHits.Add(1)
			fmt.Fprint(w, `{"choices":[]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	u, _ := url.Parse(good.URL)
	goodPort, _ := strconv.Atoi(u.Port())
	ub, _ := url.Parse(bad.URL)
	badPort, _ := strconv.Atoi(ub.Port())

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "bad", VLLMPort: badPort, MetricsPort: badPort},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (vLLM error must not trigger retry)", w.Code)
	}
	if goodHits.Load() != 0 {
		t.Fatal("good node was retried after HTTP 500 — forbidden")
	}
}

func TestProxyToVLLMOfflineNodeSkippedNextRequest(t *testing.T) {
	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			goodHits.Add(1)
			fmt.Fprint(w, `{"choices":[]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	u, _ := url.Parse(good.URL)
	goodPort, _ := strconv.Atoi(u.Port())

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "dead", VLLMPort: 1, MetricsPort: 1},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	send := func() int {
		req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	if got := send(); got != 200 {
		t.Fatalf("first request status = %d, want 200", got)
	}
	if got := send(); got != 200 {
		t.Fatalf("second request status = %d, want 200", got)
	}
	if goodHits.Load() != 2 {
		t.Fatalf("good node hits = %d, want 2 (offline dead node must be skipped on second request)", goodHits.Load())
	}
}

func TestProxyToVLLMFailsOverToDegradedNode(t *testing.T) {
	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		goodHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer good.Close()

	u, _ := url.Parse(good.URL)
	goodPort, _ := strconv.Atoi(u.Port())

	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{
			HealthInterval:      100 * time.Millisecond,
			HealthFailThreshold: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "dead", VLLMPort: 1, MetricsPort: 1},
			{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
		},
		Models: config.ModelsConfig{
			Default: "test/model",
			Available: []config.ModelConfig{
				{Name: "test/model", PipelineStages: 1},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())

	mon.Recheck("127.0.0.2")

	deadline := time.After(2 * time.Second)
	for {
		degraded := false
		for _, s := range mon.GetNodeStates() {
			if s.IP == "127.0.0.2" && s.Status == health.Degraded {
				degraded = true
			}
		}
		if degraded {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for good node to become Degraded")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (degraded survivor must be reachable)", w.Code)
	}
	if goodHits.Load() != 1 {
		t.Fatalf("good node POST hits = %d, want 1", goodHits.Load())
	}
}

func TestProxyToVLLMTruncatedResponseIs502NoRetry(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{")
			conn.Close()
		}
	}()
	badPort := ln.Addr().(*net.TCPAddr).Port

	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	u, _ := url.Parse(good.URL)
	goodPort, _ := strconv.Atoi(u.Port())

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "truncating", VLLMPort: badPort, MetricsPort: badPort},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (truncated response must not be retried)", w.Code)
	}
	if goodHits.Load() != 0 {
		t.Fatal("good node was retried after truncated response — forbidden")
	}
}

func TestProxyToVLLMStreamingHappyPath(t *testing.T) {
	var receivedBody atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody.Store(string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	payload := `{"model":"test/model","prompt":"hello","stream":true}`
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Errorf("body = %q, missing DONE", w.Body.String())
	}
	if !w.Flushed {
		t.Error("streamed response was never flushed to the client")
	}
	if b, ok := receivedBody.Load().(string); !ok || b != payload {
		t.Errorf("backend body = %q, want stream:true passthrough %q", receivedBody.Load(), payload)
	}
}

func TestProxyToVLLMStreamingRetriesOnDialFailure(t *testing.T) {
	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			goodHits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: ok\n\ndata: [DONE]\n\n")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	goodPort := extractPort(t, good.URL)

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "dead", VLLMPort: 1, MetricsPort: 1},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (retried on good node)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Errorf("body = %q, missing DONE", w.Body.String())
	}
	if goodHits.Load() != 1 {
		t.Fatalf("good node hits = %d, want 1", goodHits.Load())
	}
}

func TestProxyToVLLMStreamingEmptyStreamCommits(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 (empty stream commits recorded status)", w.Code)
	}
}

func TestProxyToVLLMStreamingJSONErrorPassthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"bad model"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (JSON error passthrough for stream request)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad model") {
		t.Errorf("body = %q, missing error payload", w.Body.String())
	}
}

func TestProxyToVLLMStreamingRetriesAfterHeadersOnlyReset(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tcp, _ := conn.(*net.TCPConn)
			io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
			tcp.SetLinger(0)
			tcp.Close()
		}
	}()
	badPort := ln.Addr().(*net.TCPAddr).Port

	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			goodHits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: ok\n\n")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	goodPort := extractPort(t, good.URL)

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "resetting", VLLMPort: badPort, MetricsPort: badPort},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (headers-then-RST must retry before first chunk)", w.Code)
	}
	if goodHits.Load() != 1 {
		t.Fatalf("good node hits = %d, want 1", goodHits.Load())
	}
}

func TestProxyToVLLMStreamingFirstByteDeadlineRetries(t *testing.T) {
	old := streamFirstByteTimeout
	streamFirstByteTimeout = 100 * time.Millisecond
	defer func() { streamFirstByteTimeout = old }()

	// Backend that responds with headers and then stalls forever.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n")
			time.Sleep(2 * time.Second)
			conn.Close()
		}
	}()
	stallPort := ln.Addr().(*net.TCPAddr).Port

	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			goodHits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: ok\n\n")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	goodPort := extractPort(t, good.URL)

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "stalling", VLLMPort: stallPort, MetricsPort: stallPort},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (first-byte deadline must retry on the good node)", w.Code)
	}
	if goodHits.Load() != 1 {
		t.Fatalf("good node hits = %d, want 1", goodHits.Load())
	}
	if time.Since(start) > time.Second {
		t.Fatalf("request took %v; deadline retry did not trigger at ~100ms", time.Since(start))
	}
}

func TestProxyToVLLMStreamingTruncatedAfterCommitNo502(t *testing.T) {
	// Amendment B + E: one COMPLETE chunk then graceful FIN (no terminal 0-chunk).
	// Amendment E: drain the POST request first — Close with unread request bytes
	// sends RST instead of FIN and races away the chunk (measured 25-40% flake
	// before drain on both toolchains). Data is delivered deterministically
	// (commits the gate), then the client hits io.ErrUnexpectedEOF on the
	// missing terminal chunk.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			br := bufio.NewReader(conn)
			if reqR, rerr := http.ReadRequest(br); rerr == nil {
				io.Copy(io.Discard, reqR.Body)
				reqR.Body.Close()
			}
			io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\nd\r\ndata: first\n\n\r\n")
			conn.Close()
		}
	}()
	badPort := ln.Addr().(*net.TCPAddr).Port

	var goodHits atomic.Int64
	good := newServerOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			goodHits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: never\n\n")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	goodPort := extractPort(t, good.URL)

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "truncating", VLLMPort: badPort, MetricsPort: badPort},
		{IP: "127.0.0.2", Name: "good", VLLMPort: goodPort, MetricsPort: goodPort},
	})

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (truncated committed stream must not become 502)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "data: first") {
		t.Errorf("body = %q, missing committed first chunk", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "data: [DONE]") {
		t.Error("truncated stream must not gain a fabricated [DONE]")
	}
	if goodHits.Load() != 0 {
		t.Fatal("good node was retried after first chunk committed — forbidden")
	}
}

func TestProxyToVLLMStreamingClientAbortPreCommit(t *testing.T) {
	// Backend accepts then stalls before responding.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			time.Sleep(2 * time.Second)
			conn.Close()
		}
	}()
	stallPort := ln.Addr().(*net.TCPAddr).Port

	h := handlerWithNodes(t, []config.NodeConfig{
		{IP: "127.0.0.1", Name: "stalling", VLLMPort: stallPort, MetricsPort: stallPort},
	})

	rec := httptest.NewRecorder()
	rec.Code = 0
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	h.ServeHTTP(rec, req)

	if rec.Code != 0 {
		t.Errorf("code = %d, want 0 (client abort before commit must be silent)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body %q written despite client abort", rec.Body.String())
	}
}

func TestProxyToVLLMOverflowPassesThrough(t *testing.T) {
	payload := strings.Repeat("x", 100)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	h, mon := newTestHandlerWithPortLimit(port, 10)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	var beforeSL, beforeCL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&beforeSL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&beforeCL)

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != payload {
		t.Errorf("client body len = %d, want %d", w.Body.Len(), len(payload))
	}
	if h.offline.skip("127.0.0.1") {
		t.Error("overflow marked node offline; overflow is not a node failure")
	}

	var afterSL, afterCL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&afterSL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&afterCL)
	if got := afterSL.GetCounter().GetValue() - beforeSL.GetCounter().GetValue(); got != 1 {
		t.Errorf("ResponsePassthroughTotal{size_limit} delta = %v, want 1 (this test must overflow the buffer, not take the Content-Length fast-path)", got)
	}
	if got := afterCL.GetCounter().GetValue() - beforeCL.GetCounter().GetValue(); got != 0 {
		t.Errorf("ResponsePassthroughTotal{content_length} delta = %v, want 0", got)
	}
}

// TestProxyToVLLMClientWriteFailureNotUpstreamFailure covers the non-streaming
// counterpart of the streaming case: an oversized response is passed through to
// a client that stops accepting bytes once commit has written the headers. The
// node delivered a valid response, so the handler must not report an upstream
// truncation and must not leave the node in the offline set.
func TestProxyToVLLMClientWriteFailureNotUpstreamFailure(t *testing.T) {
	payload := strings.Repeat("x", 100)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	h, mon := newTestHandlerWithPortLimit(port, 10)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	// failAfter 0: attemptRecorder.commit() writes the response headers before
	// any body byte, so failing the very first body write is already "after
	// commit" and is deterministic.
	fw := &failingWriter{ResponseWriter: httptest.NewRecorder(), failAfter: 0}

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	before := clientWriteFailures(t)
	logged := captureStdout(func() {
		h.ServeHTTP(fw, req)
	})

	if fw.writes < 1 {
		t.Fatalf("client writes = %d, want at least 1 so a body write was attempted", fw.writes)
	}
	rec, ok := fw.ResponseWriter.(*httptest.ResponseRecorder)
	if !ok {
		t.Fatalf("failingWriter.ResponseWriter is %T, want *httptest.ResponseRecorder", fw.ResponseWriter)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (headers were committed before the failed body write)", rec.Code)
	}
	if h.offline.skip("127.0.0.1") {
		t.Error("client write failure left the node in the offline set; a failing client is not a node failure")
	}
	if strings.Contains(logged, "response truncated after commit") {
		t.Errorf("logged an upstream truncation for a client write error:\n%s", logged)
	}
	if !strings.Contains(logged, "client write failed after commit") {
		t.Errorf("missing client-side write failure log:\n%s", logged)
	}
	if got := clientWriteFailures(t) - before; got != 1 {
		t.Errorf("ClientWriteFailuresTotal delta = %v, want 1", got)
	}
}

// TestProxyToVLLMBufferedClientWriteFailureNotUpstreamFailure covers the third
// path a client write can fail on: a small response that is fully buffered, so
// the attempt never commits early and commitResponse is what writes to the
// client. Without this, a client disconnect was visible only when the response
// happened to exceed the buffer limit.
func TestProxyToVLLMBufferedClientWriteFailureNotUpstreamFailure(t *testing.T) {
	payload := `{"id":"1"}`
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	// Limit far above the payload so the attempt buffers and takes outcomeOK.
	h, mon := newTestHandlerWithPortLimit(port, 1<<20)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	// failAfter 0: commitResponse writes the response headers before the body,
	// so failing the very first body write is already "after commit".
	fw := &failingWriter{ResponseWriter: httptest.NewRecorder(), failAfter: 0}

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	before := clientWriteFailures(t)
	logged := captureStdout(func() {
		h.ServeHTTP(fw, req)
	})

	if fw.writes < 1 {
		t.Fatalf("client writes = %d, want at least 1 so a body write was attempted", fw.writes)
	}
	rec, ok := fw.ResponseWriter.(*httptest.ResponseRecorder)
	if !ok {
		t.Fatalf("failingWriter.ResponseWriter is %T, want *httptest.ResponseRecorder", fw.ResponseWriter)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (the node answered; the client simply stopped reading)", rec.Code)
	}
	if h.offline.skip("127.0.0.1") {
		t.Error("client write failure left the node in the offline set; a failing client is not a node failure")
	}
	if strings.Contains(logged, "response truncated after commit") || strings.Contains(logged, "upstream failure") {
		t.Errorf("logged an upstream problem for a client write error:\n%s", logged)
	}
	if !strings.Contains(logged, "client write failed after commit") {
		t.Errorf("missing client-side write failure log:\n%s", logged)
	}
	if got := clientWriteFailures(t) - before; got != 1 {
		t.Errorf("ClientWriteFailuresTotal delta = %v, want 1", got)
	}
}

func TestProxyToVLLMTruncatedAfterCommitDoesNotWrite502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
				io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: 100\r\n\r\n"+strings.Repeat("y", 20))
			}(conn)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	h, mon := newTestHandlerWithPortLimit(port, 10)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Body.Len() == 0 {
		t.Fatal("client body empty; test cannot distinguish commit from a defaulted recorder code")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (already committed; never 502 after commit)", w.Code)
	}
	if h.offline.skip("127.0.0.1") {
		t.Error("post-commit truncation marked node offline")
	}
	if strings.Contains(w.Body.String(), "upstream node error") {
		t.Errorf("body contains fallback error text after commit: %q", w.Body.String())
	}
}

// TestProxyToVLLMSizeLimitOverflowThenTruncateDoesNotWrite502 covers the
// combination the Content-Length fast-path cannot: the buffer overflows
// (size_limit, not content_length) and the upstream then dies mid-body. The
// response is chunk-framed and never terminated, so the read fails with
// io.ErrUnexpectedEOF rather than the clean EOF a close-delimited body would
// give. Once the overflow prefix is committed the attempt must not be retried
// and must never be replaced with a 502.
func TestProxyToVLLMSizeLimitOverflowThenTruncateDoesNotWrite502(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
				io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n")
				io.WriteString(c, "5\r\nabcde\r\n")
				io.WriteString(c, "14\r\n"+strings.Repeat("y", 20)+"\r\n")
				// Deliberately omit the terminating 0\r\n\r\n chunk: the body
				// ends in io.ErrUnexpectedEOF, which is a truncation, not EOF.
			}(conn)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	h, mon := newTestHandlerWithPortLimit(port, 10)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	var beforeSL, beforeCL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&beforeSL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&beforeCL)

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Body.Len() == 0 {
		t.Fatal("client body empty; the overflow prefix must have been committed before the truncation")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (already committed; never 502 after commit)", w.Code)
	}
	if strings.Contains(w.Body.String(), "upstream node error") {
		t.Errorf("body contains fallback error text after commit: %q", w.Body.String())
	}
	if h.offline.skip("127.0.0.1") {
		t.Error("post-commit truncation marked node offline")
	}
	wantBody := "abcde" + strings.Repeat("y", 20)
	if w.Body.String() != wantBody {
		t.Errorf("client body = %q (len %d), want %q (len %d)", w.Body.String(), w.Body.Len(), wantBody, len(wantBody))
	}

	var afterSL, afterCL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&afterSL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&afterCL)
	if got := afterSL.GetCounter().GetValue() - beforeSL.GetCounter().GetValue(); got != 1 {
		t.Errorf("ResponsePassthroughTotal{size_limit} delta = %v, want 1", got)
	}
	if got := afterCL.GetCounter().GetValue() - beforeCL.GetCounter().GetValue(); got != 0 {
		t.Errorf("ResponsePassthroughTotal{content_length} delta = %v, want 0 (chunk-framed body must not take the Content-Length fast-path)", got)
	}
}

func TestProxyToVLLMOverflowRecordsPassthroughMetric(t *testing.T) {
	payload := strings.Repeat("x", 100)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	h, mon := newTestHandlerWithPortLimit(port, 10)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	var before dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&before)

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var after dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&after)

	got := after.GetCounter().GetValue() - before.GetCounter().GetValue()
	if got != 1 {
		t.Errorf("ResponsePassthroughTotal delta = %v, want 1", got)
	}
}

func TestProxyToVLLMSmallResponseStillBuffered(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	h, mon := newTestHandlerWithPortLimit(port, 64)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	var beforeCL, beforeSL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&beforeCL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&beforeSL)

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q", w.Body.String())
	}
	var afterCL, afterSL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&afterCL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&afterSL)
	if got := afterCL.GetCounter().GetValue() - beforeCL.GetCounter().GetValue(); got != 0 {
		t.Errorf("ResponsePassthroughTotal{content_length} delta = %v, want 0 (small response must stay buffered)", got)
	}
	if got := afterSL.GetCounter().GetValue() - beforeSL.GetCounter().GetValue(); got != 0 {
		t.Errorf("ResponsePassthroughTotal{size_limit} delta = %v, want 0 (small response must stay buffered)", got)
	}
}

func TestProxyToVLLMAbortBeforeBodySkipsPassthroughMetric(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				io.Copy(io.Discard, req.Body)
				req.Body.Close()
				io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: 100\r\n\r\n")
			}(conn)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	h, mon := newTestHandlerWithPortLimit(port, 10)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	var beforeCL, beforeSL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&beforeCL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&beforeSL)

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (no body byte was passed through; the attempt must fail)", w.Code)
	}
	var afterCL, afterSL dto.Metric
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "content_length").Write(&afterCL)
	metrics.ResponsePassthroughTotal.WithLabelValues("test/model", "size_limit").Write(&afterSL)
	if got := afterCL.GetCounter().GetValue() - beforeCL.GetCounter().GetValue(); got != 0 {
		t.Errorf("ResponsePassthroughTotal{content_length} delta = %v, want 0 (nothing was passed through)", got)
	}
	if got := afterSL.GetCounter().GetValue() - beforeSL.GetCounter().GetValue(); got != 0 {
		t.Errorf("ResponsePassthroughTotal{size_limit} delta = %v, want 0 (nothing was passed through)", got)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. Mirrors internal/log's own test helper; used here because the only
// observable difference between a client-side truncation and an upstream one is
// the log line the handler emits.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

// clientWriteFailures reads the counter for the test model so a caller can
// assert a delta across one request.
func clientWriteFailures(t *testing.T) float64 {
	t.Helper()
	var m dto.Metric
	metrics.ClientWriteFailuresTotal.WithLabelValues("test/model").Write(&m)
	return m.GetCounter().GetValue()
}

// TestProxyToVLLMStreamingClientWriteFailureNotUpstreamFailure covers a client
// that stops accepting bytes once the gate has committed the response headers.
// The node delivered a valid response, so the handler must not report an
// upstream failure and must not leave the node in the offline set.
func TestProxyToVLLMStreamingClientWriteFailureNotUpstreamFailure(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			io.WriteString(w, "data: one\n\n")
			w.(http.Flusher).Flush()
			io.WriteString(w, "data: two\n\n")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h, mon := newTestHandlerWithBackend(backend.URL)
	mon.Start()
	defer mon.Stop()
	waitForHealthy(t, mon)

	// failAfter 0: commitGate.commit() writes the response headers before the
	// body, so failing the very first body write is already "after commit" and
	// is deterministic — unlike counting writes, which depends on how the
	// upstream chunks happen to coalesce across the connection.
	fw := &failingWriter{ResponseWriter: httptest.NewRecorder(), failAfter: 0}

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"model":"test/model","prompt":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")

	before := clientWriteFailures(t)
	logged := captureStdout(func() {
		h.ServeHTTP(fw, req)
	})

	if fw.writes < 1 {
		t.Fatalf("client writes = %d, want at least 1 so a body write was attempted", fw.writes)
	}
	rec, ok := fw.ResponseWriter.(*httptest.ResponseRecorder)
	if !ok {
		t.Fatalf("failingWriter.ResponseWriter is %T, want *httptest.ResponseRecorder", fw.ResponseWriter)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (headers were committed before the failed body write)", rec.Code)
	}
	if h.offline.skip("127.0.0.1") {
		t.Error("client write failure left the node in the offline set; a failing client is not a node failure")
	}
	if strings.Contains(logged, "upstream failure") {
		t.Errorf("logged an upstream failure for a client write error:\n%s", logged)
	}
	if !strings.Contains(logged, "client write failed after commit") {
		t.Errorf("missing client-side write failure log:\n%s", logged)
	}
	if got := clientWriteFailures(t) - before; got != 1 {
		t.Errorf("ClientWriteFailuresTotal delta = %v, want 1", got)
	}
}

func TestFilterByModelExcludesUnlisted(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
		Models: config.ModelsConfig{
			Default: "a",
			Available: []config.ModelConfig{
				{Name: "a"},
				{Name: "b"},
			},
		},
	}
	h := NewHandler(cfg, health.NewMonitor(cfg), balancer.New())

	// Seed discovery state without Start(): known list without "b".
	// Reach through GetNodeStates-equivalent via the handler snapshot path.
	st := h.state.Load()
	_ = st
	// Direct unit-style check of filterByModel:
	states := []health.NodeState{
		{IP: "10.0.0.1", Models: health.ModelDiscovery{State: health.ModelsKnown, Names: []string{"a"}}},
		{IP: "10.0.0.2", Models: health.ModelDiscovery{State: health.ModelsUntried}},
	}
	kept := h.state.Load().filterByModel(states, "b")
	if len(kept) != 1 || kept[0].IP != "10.0.0.2" {
		t.Errorf("filterByModel(b) = %+v, want only 10.0.0.2 (untried)", kept)
	}
	kept = h.state.Load().filterByModel(states, "a")
	if len(kept) != 2 {
		t.Errorf("filterByModel(a) = %+v, want both nodes", kept)
	}
}

func TestFilterByModelStateMatrix(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
	}
	h := NewHandler(cfg, health.NewMonitor(cfg), balancer.New())
	st := h.state.Load()

	states := []health.NodeState{
		{IP: "untried", Models: health.ModelDiscovery{State: health.ModelsUntried}},
		{IP: "known-has", Models: health.ModelDiscovery{State: health.ModelsKnown, Names: []string{"m"}}},
		{IP: "known-miss", Models: health.ModelDiscovery{State: health.ModelsKnown, Names: []string{"other"}}},
		{IP: "degraded-has", Models: health.ModelDiscovery{State: health.ModelsDegraded, Names: []string{"m"}}},
		{IP: "empty", Models: health.ModelDiscovery{State: health.ModelsKnownEmpty}},
		{IP: "expired", Models: health.ModelDiscovery{State: health.ModelsExpired, Names: []string{"m"}}},
	}
	kept := st.filterByModel(states, "m")
	want := map[string]bool{"untried": true, "known-has": true, "degraded-has": true}
	if len(kept) != len(want) {
		t.Fatalf("kept = %+v, want %d nodes", kept, len(want))
	}
	for _, s := range kept {
		if !want[s.IP] {
			t.Errorf("unexpected kept node %q", s.IP)
		}
	}
}

func TestProxyToVLLMModelFilter503AndCounter(t *testing.T) {
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000}},
		Models: config.ModelsConfig{
			Default: "a",
			Available: []config.ModelConfig{
				{Name: "a"},
				{Name: "b"},
			},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())

	// Force known-empty via two applyResult empties through a tiny loop:
	// simulate by calling the unexported path is not possible from api tests.
	// Instead run one real node that reports no models twice — use httptest.
	// Simpler deterministic approach: rebuild handler with seeded monitor state
	// using NewMonitorWithState.
	seeded := health.NewMonitorWithState(cfg, []health.NodeState{{
		IP:     "10.0.0.1",
		Name:   "n1",
		Status: health.Healthy,
		Models: health.ModelDiscovery{State: health.ModelsKnown, Names: []string{"a"}},
	}})
	h = NewHandler(cfg, seeded, balancer.New())

	before := counterValue(t, metrics.ModelFilterExclusionsTotal, "b", "missing")

	body := `{"model":"b","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no healthy node available") {
		t.Errorf("body = %q, want unchanged 503 body", w.Body.String())
	}
	after := counterValue(t, metrics.ModelFilterExclusionsTotal, "b", "missing")
	if after-before < 1 {
		t.Errorf("gateway_model_filter_exclusions_total{b,missing} delta = %v, want >= 1", after-before)
	}
}

func TestProxyToVLLMUntriedFilterIsIdentity(t *testing.T) {
	// No Start(): every node is untried. A request for an unknown model must
	// NOT be rejected by the model filter (it may still 503 for other reasons
	// only if there are no nodes).
	cfg := &config.Config{
		Cluster: config.ClusterConfig{Name: "test"},
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "10.0.0.1", Name: "n1", VLLMPort: 1}},
		Models: config.ModelsConfig{
			Default: "a",
			Available: []config.ModelConfig{
				{Name: "a"},
			},
		},
	}
	mon := health.NewMonitor(cfg) // NOT started
	h := NewHandler(cfg, mon, balancer.New())

	states := h.state.Load().filterByModel(mon.GetNodeStates(), "not/in/config")
	if len(states) != 1 {
		t.Fatalf("untried filter dropped nodes: %+v", states)
	}
}

// counterValue reads a CounterVec sample sum for the given label values.
func counterValue(t *testing.T, vec *prometheus.CounterVec, labelValues ...string) float64 {
	t.Helper()
	c, err := vec.GetMetricWithLabelValues(labelValues...)
	if err != nil {
		t.Fatal(err)
	}
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func TestHealthEndpointModelDetails(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000},
			{IP: "10.0.0.2", Name: "n2", VLLMPort: 8000},
		},
	}
	mon := health.NewMonitorWithState(cfg, []health.NodeState{
		{
			IP:     "10.0.0.1",
			Name:   "n1",
			Status: health.Healthy,
			Models: health.ModelDiscovery{
				State:     health.ModelsKnown,
				Names:     []string{"a"},
				FetchedAt: time.Now().Add(-2 * time.Second),
			},
		},
		{
			IP:     "10.0.0.2",
			Name:   "n2",
			Status: health.Healthy,
			Models: health.ModelDiscovery{State: health.ModelsExpired, LastError: "down"},
		},
	})
	cfg2 := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "n1", VLLMPort: 8000},
			{IP: "10.0.0.2", Name: "n2", VLLMPort: 8000},
		},
	}
	mon = health.NewMonitorWithState(cfg2, mon.GetNodeStates())
	h := NewHandler(cfg2, mon, balancer.New())

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Status   string `json:"status"`
		Nodes    int    `json:"nodes"`
		Healthy  int    `json:"healthy"`
		ModelsOK int    `json:"models_ok"`
		Details  []struct {
			IP     string `json:"ip"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Models struct {
				State      string   `json:"state"`
				Names      []string `json:"names"`
				AgeSeconds *float64 `json:"age_seconds"`
				Fails      int      `json:"fails"`
				LastError  string   `json:"last_error"`
			} `json:"models"`
		} `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	// Existing fields must remain (and status is health-only).
	if resp.Nodes != 2 || resp.Healthy != 2 {
		t.Errorf("nodes/healthy = %d/%d, want 2/2", resp.Nodes, resp.Healthy)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok (model state must not affect it)", resp.Status)
	}
	if resp.ModelsOK != 1 {
		t.Errorf("models_ok = %d, want 1", resp.ModelsOK)
	}
	if len(resp.Details) != 2 {
		t.Fatalf("details = %d, want 2", len(resp.Details))
	}

	var known, expired *bool
	_ = known
	_ = expired
	for _, d := range resp.Details {
		switch d.IP {
		case "10.0.0.1":
			if d.Models.State != "known" {
				t.Errorf("n1 state = %q, want known", d.Models.State)
			}
			if len(d.Models.Names) != 1 || d.Models.Names[0] != "a" {
				t.Errorf("n1 names = %v, want [a]", d.Models.Names)
			}
			if d.Models.AgeSeconds == nil {
				t.Error("n1 age_seconds nil, want number")
			}
		case "10.0.0.2":
			if d.Models.State != "expired" {
				t.Errorf("n2 state = %q, want expired", d.Models.State)
			}
			if d.Models.Names != nil {
				t.Errorf("n2 names = %v, want null/nil", d.Models.Names)
			}
			if d.Models.AgeSeconds != nil {
				t.Error("n2 age_seconds set, want null")
			}
			if d.Models.LastError != "down" {
				t.Errorf("n2 last_error = %q, want down", d.Models.LastError)
			}
		}
	}
}

func TestHealthEndpointLegacyFieldsUnchanged(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	for _, key := range []string{"status", "nodes", "healthy"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing legacy field %q", key)
		}
	}
	if _, ok := resp["models_ok"]; !ok {
		t.Error("missing models_ok")
	}
	if _, ok := resp["details"]; !ok {
		t.Error("missing details")
	}
}

func newFakeVLLM(t *testing.T, ip string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp4", ip+":0")
	if err != nil {
		t.Fatalf("listen on %s: %v", ip, err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestProxyToVLLM503ElevatesNodeAndHealthReflectsIt(t *testing.T) {
	vllmA := newFakeVLLM(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"engine busy"}`))
	}))
	vllmB := newFakeVLLM(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"1","object":"text_completion","created":1,"model":"test/model","choices":[{"index":0,"text":"ok","finish_reason":"stop"}]}`))
	}))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:                100 * time.Millisecond,
			HealthFailThreshold:           3,
			RequestOverloadThresholdValue: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "a", VLLMPort: extractPort(t, vllmA.URL)},
			{IP: "127.0.0.2", Name: "b", VLLMPort: extractPort(t, vllmB.URL)},
		},
		Models: config.ModelsConfig{
			Default:   "test/model",
			Available: []config.ModelConfig{{Name: "test/model", PipelineStages: 1}},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())

	body := `{"model":"test/model","prompt":"hi","max_tokens":1}`
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	healthJSON := func() map[string]interface{} {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var out map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("health JSON: %v", err)
		}
		return out
	}

	// Round-robin alternates a,b. Three hits land on A after six requests;
	// A's streak reaches 3 and its served status must read degraded.
	for i := 0; i < 6; i++ {
		w := do()
		if i%2 == 0 && w.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d to node A: code = %d, want 503", i, w.Code)
		}
	}
	waitForConditionProxy(t, 2*time.Second, "node A degraded on /health", func() bool {
		out := healthJSON()
		details, ok := out["details"].([]interface{})
		if !ok {
			return false
		}
		for _, d := range details {
			nd := d.(map[string]interface{})
			if nd["name"] == "a" {
				return nd["status"] == "degraded" && nd["req_streak"].(float64) == 3
			}
		}
		return false
	})

	// All subsequent requests must be served by B (healthy A still degraded).
	for i := 0; i < 4; i++ {
		if w := do(); w.Code != http.StatusOK {
			t.Fatalf("post-elevation request %d: code = %d, want 200 from B", i, w.Code)
		}
	}
}

func waitForConditionProxy(t *testing.T, timeout time.Duration, desc string, check func() bool) {
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

// Streaming site: a 503 on a stream:true request is a status-bearing, err==nil
// attempt; the gateway must feed it to the monitor before any chunk commits.
func TestProxyToVLLMStreaming503ElevatesNode(t *testing.T) {
	vllmA := newFakeVLLM(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"engine busy"}`))
	}))
	vllmB := newFakeVLLM(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"test/model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"))
	}))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:                100 * time.Millisecond,
			HealthFailThreshold:           3,
			RequestOverloadThresholdValue: 3,
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "a", VLLMPort: extractPort(t, vllmA.URL)},
			{IP: "127.0.0.2", Name: "b", VLLMPort: extractPort(t, vllmB.URL)},
		},
		Models: config.ModelsConfig{
			Default:   "test/model",
			Available: []config.ModelConfig{{Name: "test/model", PipelineStages: 1}},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())

	body := `{"model":"test/model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	do := func() { // 6 requests: 3 land on A (503), 3 on B (200 stream)
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
	for i := 0; i < 6; i++ {
		do()
	}
	waitForConditionProxy(t, 2*time.Second, "node A distributed degraded via streaming 503s", func() bool {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var out map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			return false
		}
		details, ok := out["details"].([]interface{})
		if !ok {
			return false
		}
		for _, d := range details {
			nd := d.(map[string]interface{})
			if nd["name"] == "a" {
				return nd["status"] == "degraded" && nd["req_streak"].(float64) == 3
			}
		}
		return false
	})
}

// Committed site: an oversized 503 body is committed to the client (pass-through
// path); the gateway must still report the status to the monitor.
func TestProxyToVLLMCommitted503ElevatesNode(t *testing.T) {
	big := []byte(`{"error":"engine busy","detail":"` + strings.Repeat("x", 4096) + `"}`)
	vllmA := newFakeVLLM(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write(big) // 4 KiB, over the 1 KiB buffer limit below
	}))
	vllmB := newFakeVLLM(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"1","object":"text_completion","created":1,"model":"test/model","choices":[{"index":0,"text":"ok","finish_reason":"stop"}]}`))
	}))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			HealthInterval:                100 * time.Millisecond,
			HealthFailThreshold:           3,
			RequestOverloadThresholdValue: 3,
			MaxBufferedResponseBytes:      1024, // force pass-through commit above this
		},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "a", VLLMPort: extractPort(t, vllmA.URL)},
			{IP: "127.0.0.2", Name: "b", VLLMPort: extractPort(t, vllmB.URL)},
		},
		Models: config.ModelsConfig{
			Default:   "test/model",
			Available: []config.ModelConfig{{Name: "test/model", PipelineStages: 1}},
		},
	}
	mon := health.NewMonitor(cfg)
	h := NewHandler(cfg, mon, balancer.New())

	// Warm up: Teach the health state that these nodes are fine without a live probe.
	// (The monitor is NOT Started — same isolation rationale as Task 4; nodes start Healthy.)
	body := `{"model":"test/model","prompt":"hi","max_tokens":1}`
	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	for i := 0; i < 6; i++ {
		if w := do(); i%2 == 0 && w.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d to node A: code = %d, want 503", i, w.Code)
		}
	}
	waitForConditionProxy(t, 2*time.Second, "node A degraded via committed oversized 503s", func() bool {
		req := httptest.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var out map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			return false
		}
		details, ok := out["details"].([]interface{})
		if !ok {
			return false
		}
		for _, d := range details {
			nd := d.(map[string]interface{})
			if nd["name"] == "a" {
				return nd["status"] == "degraded" && nd["req_streak"].(float64) == 3
			}
		}
		return false
	})
}
