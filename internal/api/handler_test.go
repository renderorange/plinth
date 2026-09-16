package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
