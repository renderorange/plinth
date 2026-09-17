package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"distributed-vram/internal/balancer"
	"distributed-vram/internal/config"
	"distributed-vram/internal/health"
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
	h := newTestHandler()
	old := maxBodyBytes
	maxBodyBytes = 16
	defer func() { maxBodyBytes = old }()

	body := `{"model":"test/model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d (body too large)", w.Code, http.StatusRequestEntityTooLarge)
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

func TestStatusRecorderCapturesCode(t *testing.T) {
	tests := []struct {
		name       string
		writeCode  int
		wantStatus int
	}{
		{"ok", 200, 200},
		{"internal error", 500, 500},
		{"bad gateway", 502, 502},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			sr.WriteHeader(tt.writeCode)
			if sr.status != tt.wantStatus {
				t.Errorf("statusRecorder.status = %d, want %d", sr.status, tt.wantStatus)
			}
		})
	}
}

func TestStatusRecorderFlushNoPanic(t *testing.T) {
	w := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	sr.Flush()
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flushRecorder) Flush() {
	f.flushed = true
	f.ResponseRecorder.Flush()
}

func TestStatusRecorderFlushDelegates(t *testing.T) {
	fr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	sr := &statusRecorder{ResponseWriter: fr, status: http.StatusOK}
	sr.Flush()
	if !fr.flushed {
		t.Error("Flush was not delegated to underlying ResponseWriter")
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

	payload := `{"model":"test/model","prompt":"hi"}`
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
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
