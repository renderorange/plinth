package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
)

func reloadHandlerConfigs(t *testing.T) (*Handler, *config.Config, *config.Config) {
	t.Helper()
	cfgA := &config.Config{
		Cluster: config.ClusterConfig{Name: "first"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Models: config.ModelsConfig{
			Default: "model-a",
			Available: []config.ModelConfig{
				{Name: "model-a"},
			},
		},
	}
	cfgB := &config.Config{
		Cluster: config.ClusterConfig{Name: "second"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.2", Name: "node-2", Ring: "ring-b"},
		},
		Models: config.ModelsConfig{
			Default: "model-b",
			Available: []config.ModelConfig{
				{Name: "model-b", Ring: "ring-b"},
			},
		},
	}
	mon := health.NewMonitor(cfgA)
	return NewHandler(cfgA, mon, balancer.New()), cfgA, cfgB
}

func TestUpdateConfigSwapsRouting(t *testing.T) {
	h, _, cfgB := reloadHandlerConfigs(t)
	newMon := health.NewMonitor(cfgB)
	h.UpdateConfig(cfgB, newMon)

	if got := h.Config(); got != cfgB {
		t.Fatalf("Config() = %p, want cfgB %p", got, cfgB)
	}

	states := []health.NodeState{
		{IP: "10.0.0.2", Name: "node-2", Status: health.Healthy},
	}
	filtered := h.filterByRing(states, "model-b")
	if len(filtered) != 1 || filtered[0].IP != "10.0.0.2" {
		t.Fatalf("filterByRing after swap = %+v, want 10.0.0.2 routed to ring-b", filtered)
	}

	filtered = h.filterByRing([]health.NodeState{{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy}}, "model-a")
	if len(filtered) != 0 {
		t.Fatalf("filterByRing(model-a) after swap = %+v, want empty (10.0.0.1 removed from serving config)", filtered)
	}
}

func TestUpdateConfigHeldSnapshotStaysCoherent(t *testing.T) {
	h, cfgA, cfgB := reloadHandlerConfigs(t)
	held := h.state.Load()

	newMon := health.NewMonitor(cfgB)
	h.UpdateConfig(cfgB, newMon)

	if held.cfg != cfgA {
		t.Error("held snapshot cfg changed after swap")
	}
	filtered := held.filterByRing([]health.NodeState{{IP: "10.0.0.1", Name: "node-1", Status: health.Healthy}}, "model-a")
	if len(filtered) != 1 {
		t.Errorf("held snapshot routing = %+v, want 10.0.0.1 in standalone pool", filtered)
	}
	if got := h.state.Load(); got == held {
		t.Error("handler still serves the old snapshot after UpdateConfig")
	}
}

func TestModelsEndpointAfterSwapServesNewConfig(t *testing.T) {
	h, _, cfgB := reloadHandlerConfigs(t)
	h.UpdateConfig(cfgB, health.NewMonitor(cfgB))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "model-b") {
		t.Errorf("models response %q missing model-b", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "model-a") {
		t.Errorf("models response %q still contains model-a from cfgA", w.Body.String())
	}
	if h.Config() != cfgB {
		t.Error("h.Config() did not reflect the swap")
	}
}

func TestModelsDuringConcurrentSwaps(t *testing.T) {
	cfgA := &config.Config{
		Cluster: config.ClusterConfig{Name: "a"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.1", Name: "node-1"},
		},
		Models: config.ModelsConfig{
			Default: "model-a",
			Available: []config.ModelConfig{
				{Name: "model-a"},
			},
		},
	}
	cfgB := &config.Config{
		Cluster: config.ClusterConfig{Name: "b"},
		Nodes: []config.NodeConfig{
			{IP: "10.0.0.2", Name: "node-2"},
		},
		Models: config.ModelsConfig{
			Default: "model-b",
			Available: []config.ModelConfig{
				{Name: "model-b"},
			},
		},
	}
	monA := health.NewMonitor(cfgA)
	monB := health.NewMonitor(cfgB)
	h := NewHandler(cfgB, monB, balancer.New())

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		useB := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if useB {
				h.UpdateConfig(cfgB, monB)
				useB = false
			} else {
				h.UpdateConfig(cfgA, monA)
				useB = true
			}
		}
	}()

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("request %d status = %d, want 200", i, w.Code)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("request %d invalid JSON: %v", i, err)
		}
		if len(resp.Data) != 1 {
			t.Fatalf("request %d data = %+v, want exactly 1 model (no torn reads)", i, resp.Data)
		}
		if id := resp.Data[0].ID; id != "model-a" && id != "model-b" {
			t.Fatalf("request %d id = %q, want model-a or model-b", i, id)
		}
	}
	close(stop)
	wg.Wait()
}

// gatedBody blocks its first Read until released, and signals when that Read
// is reached. It pins a request inside proxyToVLLM after the snapshot load
// (handler.go reads the body only after loading the per-request snapshot).
type gatedBody struct {
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
	data        []byte
	off         int
}

func (b *gatedBody) Read(p []byte) (int, error) {
	b.reachedOnce.Do(func() { close(b.reached) })
	<-b.release
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *gatedBody) Close() error { return nil }

func (b *gatedBody) Release() {
	b.releaseOnce.Do(func() { close(b.release) })
}

// TestRequestKeepsSingleSnapshotAcrossReload pins a completion request after
// proxyToVLLM loads its per-request snapshot, swaps the config mid-request,
// then releases the body. The request must finish entirely against the old
// snapshot: under cfgA the model is standalone and routes to the local
// backend, while a second snapshot load mid-request would apply cfgB's ring
// rules and produce 503 (no node in ring-b is healthy per the old monitor).
func TestRequestKeepsSingleSnapshotAcrossReload(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"cmpl-1","object":"text_completion","choices":[]}`)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)
	cfgA := &config.Config{
		Cluster: config.ClusterConfig{Name: "a"},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.1", Name: "node-1", VLLMPort: port, MetricsPort: port},
		},
		Models: config.ModelsConfig{
			Available: []config.ModelConfig{{Name: "model-a"}},
		},
	}
	cfgB := &config.Config{
		Cluster: config.ClusterConfig{Name: "b"},
		Nodes: []config.NodeConfig{
			{IP: "127.0.0.2", Name: "node-2", Ring: "ring-b"},
		},
		Models: config.ModelsConfig{
			Available: []config.ModelConfig{{Name: "model-a", Ring: "ring-b"}},
		},
	}
	h := NewHandler(cfgA, health.NewMonitor(cfgA), balancer.New())

	body := &gatedBody{
		reached: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte(`{"model":"model-a"}`),
	}
	req := httptest.NewRequest("POST", "/v1/completions", body)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, req)
		close(done)
	}()
	t.Cleanup(func() { body.Release(); <-done })

	<-body.reached
	h.UpdateConfig(cfgB, health.NewMonitor(cfgB))
	body.Release()
	<-done

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (request must finish against the snapshot captured at entry)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "cmpl-1") {
		t.Fatalf("body = %q, want the backend completion", w.Body.String())
	}
}
