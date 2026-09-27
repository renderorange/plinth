package health

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"plinth/internal/config"
)

func TestApplyResult(t *testing.T) {
	now := time.Now()
	errBoom := errors.New("boom")

	tests := []struct {
		name          string
		start         ModelDiscovery
		names         []string
		fetchErr      error
		threshold     int
		wantState     ModelState
		wantNames     []string
		wantFails     int
		wantEmpties   int
		wantChanged   bool
		wantFetchedAt bool
	}{
		{
			name:          "untried success non-empty",
			start:         ModelDiscovery{State: ModelsUntried},
			names:         []string{"a"},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a"},
			wantChanged:   true,
			wantFetchedAt: true,
		},
		{
			name:        "untried fail stays untried",
			start:       ModelDiscovery{State: ModelsUntried},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsUntried,
			wantFails:   1,
			wantChanged: false,
		},
		{
			name:        "untried fails reach N expires",
			start:       ModelDiscovery{State: ModelsUntried, Fails: 2},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsExpired,
			wantFails:   3,
			wantChanged: false,
		},
		{
			name:          "known fail keeps list and degrades",
			start:         ModelDiscovery{State: ModelsKnown, Names: []string{"a"}, FetchedAt: now},
			fetchErr:      errBoom,
			threshold:     3,
			wantState:     ModelsDegraded,
			wantNames:     []string{"a"},
			wantFails:     1,
			wantChanged:   false,
			wantFetchedAt: true,
		},
		{
			name:        "degraded fail at N expires keeps list",
			start:       ModelDiscovery{State: ModelsDegraded, Names: []string{"a"}, Fails: 2},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsExpired,
			wantNames:   []string{"a"},
			wantFails:   3,
			wantChanged: false,
		},
		{
			name:          "degraded success recovers",
			start:         ModelDiscovery{State: ModelsDegraded, Names: []string{"a"}, Fails: 2},
			names:         []string{"a", "b"},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a", "b"},
			wantChanged:   true,
			wantFetchedAt: true,
		},
		{
			name:          "first empty keeps prior state",
			start:         ModelDiscovery{State: ModelsKnown, Names: []string{"a"}, FetchedAt: now},
			names:         []string{},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a"},
			wantEmpties:   1,
			wantChanged:   false,
			wantFetchedAt: true,
		},
		{
			name:          "second empty enters known_empty and clears",
			start:         ModelDiscovery{State: ModelsKnown, Names: []string{"a"}, Empties: 1, FetchedAt: now},
			names:         []string{},
			threshold:     3,
			wantState:     ModelsKnownEmpty,
			wantNames:     nil,
			wantEmpties:   2,
			wantChanged:   true,
			wantFetchedAt: true,
		},
		{
			name:          "empty while untried stays untried",
			start:         ModelDiscovery{State: ModelsUntried},
			names:         []string{},
			threshold:     3,
			wantState:     ModelsUntried,
			wantEmpties:   1,
			wantChanged:   false,
			wantFetchedAt: true,
		},
		{
			name:        "known_empty fail stays known_empty",
			start:       ModelDiscovery{State: ModelsKnownEmpty, Empties: 2},
			fetchErr:    errBoom,
			threshold:   3,
			wantState:   ModelsKnownEmpty,
			wantFails:   1,
			wantEmpties: 0,
			wantChanged: false,
		},
		{
			name:          "success resets counters",
			start:         ModelDiscovery{State: ModelsDegraded, Names: []string{"a"}, Fails: 2, Empties: 1, LastError: "x"},
			names:         []string{"a"},
			threshold:     3,
			wantState:     ModelsKnown,
			wantNames:     []string{"a"},
			wantChanged:   false,
			wantFetchedAt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.start
			prevFetched := d.FetchedAt
			changed := d.applyResult(tt.names, tt.fetchErr, tt.threshold, now)

			if d.State != tt.wantState {
				t.Errorf("State = %v, want %v", d.State, tt.wantState)
			}
			if len(d.Names) != len(tt.wantNames) {
				t.Fatalf("Names = %v, want %v", d.Names, tt.wantNames)
			}
			for i := range tt.wantNames {
				if d.Names[i] != tt.wantNames[i] {
					t.Errorf("Names = %v, want %v", d.Names, tt.wantNames)
				}
			}
			if d.Fails != tt.wantFails {
				t.Errorf("Fails = %d, want %d", d.Fails, tt.wantFails)
			}
			if d.Empties != tt.wantEmpties {
				t.Errorf("Empties = %d, want %d", d.Empties, tt.wantEmpties)
			}
			if changed != tt.wantChanged {
				t.Errorf("listChanged = %v, want %v", changed, tt.wantChanged)
			}
			if tt.wantFetchedAt {
				if d.FetchedAt.IsZero() {
					t.Error("FetchedAt still zero, want set")
				}
			} else if d.FetchedAt != prevFetched {
				t.Errorf("FetchedAt changed on failure: %v -> %v", prevFetched, d.FetchedAt)
			}
			if tt.fetchErr != nil {
				if d.LastError == "" {
					t.Error("LastError empty after fetch error")
				}
			} else if d.LastError != "" {
				t.Errorf("LastError = %q after success, want empty", d.LastError)
			}
		})
	}
}

func TestParseModelList(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{
			name: "ids extracted",
			body: `{"object":"list","data":[{"id":"a","object":"model"},{"id":"b"}]}`,
			want: []string{"a", "b"},
		},
		{
			name: "empty data",
			body: `{"object":"list","data":[]}`,
			want: []string{},
		},
		{
			name:    "bad json",
			body:    `{not json`,
			wantErr: true,
		},
		{
			name:    "wrong shape",
			body:    `{"data":"nope"}`,
			wantErr: true,
		},
		{
			name:    "missing data field",
			body:    `{"error":{"message":"boom"}}`,
			wantErr: true,
		},
		{
			name:    "null body",
			body:    `null`,
			wantErr: true,
		},
		{
			name:    "empty object",
			body:    `{}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModelList(strings.NewReader(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("err = nil, want error (got %v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestApplyModelsResultLogsAndMetrics(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n1", VLLMPort: 1, Ring: "ring-a"}},
		Models: config.ModelsConfig{
			Default: "miss/model",
			Available: []config.ModelConfig{
				{Name: "miss/model", Ring: "ring-a"},
				{Name: "have/model", Ring: "ring-a"},
			},
		},
	}
	m := NewMonitor(cfg)

	out := captureHealthOutput(t, func() {
		m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil)
	})
	if !strings.Contains(out, "models discovered") {
		t.Errorf("missing discovery log, got %s", out)
	}
	if !strings.Contains(out, "configured model not served by node") {
		t.Errorf("missing config-mismatch warn, got %s", out)
	}
	if !strings.Contains(out, "miss/model") {
		t.Errorf("mismatch warn should name the model, got %s", out)
	}

	states := m.GetNodeStates()
	if states[0].Models.State != ModelsKnown {
		t.Errorf("State = %v, want known", states[0].Models.State)
	}
}

func TestApplyModelsResultRingFilteredDefaultDoesNotWarn(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n1", VLLMPort: 1, Ring: "b"}},
		Models: config.ModelsConfig{
			Default: "dflt",
			Available: []config.ModelConfig{
				{Name: "dflt", Ring: "a"},
			},
		},
	}
	m := NewMonitor(cfg)

	out := captureHealthOutput(t, func() {
		m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil)
	})
	if !strings.Contains(out, "node model list changed") {
		t.Fatalf("warn path did not run, got %s", out)
	}
	if strings.Contains(out, "configured model not served by node") {
		t.Errorf("ring-filtered default must not warn, got %s", out)
	}
}

func TestApplyModelsResultDefaultAbsentFromAvailableWarns(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n1", VLLMPort: 1, Ring: "b"}},
		Models: config.ModelsConfig{
			Default: "dflt",
			Available: []config.ModelConfig{
				{Name: "other/model", Ring: "a"},
			},
		},
	}
	m := NewMonitor(cfg)

	out := captureHealthOutput(t, func() {
		m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil)
	})
	if !strings.Contains(out, "configured model not served by node") {
		t.Errorf("missing config-mismatch warn, got %s", out)
	}
	if !strings.Contains(out, "dflt") {
		t.Errorf("mismatch warn should name the model, got %s", out)
	}
}

func TestApplyModelsResultTransitionLogs(t *testing.T) {
	fetchFail := errors.New("fetch failed")
	newMonitor := func(threshold int) *Monitor {
		cfg := &config.Config{
			Gateway: config.GatewayConfig{HealthInterval: time.Hour, HealthFailThreshold: threshold},
			Nodes:   []config.NodeConfig{{IP: "127.0.0.1", Name: "n1", VLLMPort: 1}},
		}
		return NewMonitor(cfg)
	}

	t.Run("untried_to_known", func(t *testing.T) {
		m := newMonitor(3)
		out := captureHealthOutput(t, func() {
			m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil)
		})
		if !strings.Contains(out, "models discovered") {
			t.Errorf("missing models discovered, got %s", out)
		}
		if strings.Contains(out, "model discovery recovered") {
			t.Errorf("untried->known must not log recovered, got %s", out)
		}
	})

	t.Run("degraded_to_known", func(t *testing.T) {
		m := newMonitor(3)
		m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil) // untried -> known
		m.applyModelsResult("127.0.0.1", nil, fetchFail)              // known -> degraded
		out := captureHealthOutput(t, func() {
			m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil) // degraded -> known
		})
		if !strings.Contains(out, "model discovery recovered") {
			t.Errorf("missing model discovery recovered, got %s", out)
		}
		if strings.Contains(out, "models discovered") {
			t.Errorf("degraded->known must not log models discovered, got %s", out)
		}
	})

	t.Run("expired_to_known", func(t *testing.T) {
		m := newMonitor(2)
		m.applyModelsResult("127.0.0.1", nil, fetchFail) // untried, fails 1
		m.applyModelsResult("127.0.0.1", nil, fetchFail) // expired, fails 2
		out := captureHealthOutput(t, func() {
			m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil) // expired -> known
		})
		if !strings.Contains(out, "model discovery recovered") {
			t.Errorf("missing model discovery recovered, got %s", out)
		}
		if strings.Contains(out, "models discovered") {
			t.Errorf("expired->known must not log models discovered, got %s", out)
		}
	})

	t.Run("known_empty_to_known", func(t *testing.T) {
		m := newMonitor(3)
		m.applyModelsResult("127.0.0.1", nil, nil) // untried, empties 1
		m.applyModelsResult("127.0.0.1", nil, nil) // known_empty, empties 2
		out := captureHealthOutput(t, func() {
			m.applyModelsResult("127.0.0.1", []string{"have/model"}, nil) // known_empty -> known
		})
		if !strings.Contains(out, "model discovery recovered") {
			t.Errorf("missing model discovery recovered, got %s", out)
		}
		if strings.Contains(out, "models discovered") {
			t.Errorf("known_empty->known must not log models discovered, got %s", out)
		}
	})
}

func TestFetchModelsAndDiscoverAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"svc/model"}]}`))
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: 20 * time.Millisecond, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: host, Name: "n1", VLLMPort: port}},
	}
	m := NewMonitor(cfg)
	m.Start()
	t.Cleanup(m.Stop)

	waitForCondition(t, 2*time.Second, "node model list discovered", func() bool {
		states := m.GetNodeStates()
		return len(states) == 1 && states[0].Models.State == ModelsKnown
	})
	states := m.GetNodeStates()
	if len(states[0].Models.Names) != 1 || states[0].Models.Names[0] != "svc/model" {
		t.Errorf("Names = %v, want [svc/model]", states[0].Models.Names)
	}
}

func TestHungModelFetchDoesNotBlockHealth(t *testing.T) {
	release := make(chan struct{})
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			<-release // hang until test ends
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer models.Close()
	defer close(release)

	host, port := hostPort(t, models.URL)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: 20 * time.Millisecond, HealthFailThreshold: 3},
		Nodes:   []config.NodeConfig{{IP: host, Name: "n1", VLLMPort: port, MetricsPort: port}},
	}
	m := NewMonitor(cfg)
	m.Start()
	t.Cleanup(m.Stop)

	// Health (/health 200) must keep updating while /v1/models hangs.
	var first, second time.Time
	waitForCondition(t, 2*time.Second, "first health check recorded", func() bool {
		states := m.GetNodeStates()
		return len(states) == 1 && !states[0].LastCheck.IsZero()
	})
	first = m.GetNodeStates()[0].LastCheck
	waitForCondition(t, 2*time.Second, "health checks advance", func() bool {
		st := m.GetNodeStates()[0]
		return st.LastCheck.After(first)
	})
	second = m.GetNodeStates()[0].LastCheck
	if !second.After(first) {
		t.Fatalf("LastCheck did not advance: %v -> %v", first, second)
	}
}

func TestModelDiscoveryFailureCountsToExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health probes must keep succeeding so the Status assertion proves
		// that model-listing failures alone never touch health state.
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{HealthInterval: 15 * time.Millisecond, HealthFailThreshold: 2},
		Nodes:   []config.NodeConfig{{IP: host, Name: "n1", VLLMPort: port}},
	}
	m := NewMonitor(cfg)
	m.Start()
	t.Cleanup(m.Stop)

	waitForCondition(t, 2*time.Second, "models expired", func() bool {
		states := m.GetNodeStates()
		return len(states) == 1 && states[0].Models.State == ModelsExpired
	})
	st := m.GetNodeStates()[0]
	if st.Status != Healthy {
		t.Errorf("Status = %v, want Healthy (listing must not touch health)", st.Status)
	}
	if st.Models.Allows("x") {
		t.Error("expired node must not allow models")
	}
}

// captureHealthOutput captures process stdout around fn (log package writes JSON to stdout).
// If internal/log exposes captureOutput, prefer copying that pattern here.
func captureHealthOutput(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func hostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}
