package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"plinth/internal/api"
	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func newTestGateway(t *testing.T, path string) (*api.Handler, *health.Monitor) {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	mon := health.NewMonitor(cfg)
	return api.NewHandler(cfg, mon, balancer.New()), mon
}

const baseConfig = `
[cluster]
name = "first"

[gateway]
health_interval = "1s"
health_fail_threshold = 3

[[nodes]]
ip = "127.0.0.1"
name = "node-1"
vllm_port = 1
metrics_port = 2
`

func TestReloadConfigAppliesNewConfig(t *testing.T) {
	path := writeTestConfig(t, baseConfig)
	handler, mon := newTestGateway(t, path)
	curMon.Store(mon)
	mon.Start()
	t.Cleanup(func() { curMon.Load().Stop() })

	pathB := writeTestConfig(t, strings.Replace(baseConfig, `name = "first"`, `name = "second"`, 1))
	reloadConfig(context.Background(), handler, pathB)

	if got := handler.Config().Cluster.Name; got != "second" {
		t.Fatalf("cluster = %q, want second", got)
	}
	if curMon.Load() == mon {
		t.Fatal("curMon not replaced by reload")
	}
}

func TestReloadConfigInvalidKeepsCurrent(t *testing.T) {
	path := writeTestConfig(t, baseConfig)
	handler, mon := newTestGateway(t, path)
	curMon.Store(mon)
	mon.Start()
	t.Cleanup(func() { curMon.Load().Stop() })

	pathB := writeTestConfig(t, "this is not [[[ valid toml")
	reloadConfig(context.Background(), handler, pathB)

	if got := handler.Config().Cluster.Name; got != "first" {
		t.Fatalf("cluster = %q, want first (invalid reload must keep current)", got)
	}
	if curMon.Load() != mon {
		t.Fatal("curMon replaced despite invalid reload")
	}
}

func TestReloadConfigCanceledCtxIsNoop(t *testing.T) {
	path := writeTestConfig(t, baseConfig)
	handler, mon := newTestGateway(t, path)
	curMon.Store(mon)
	mon.Start()
	t.Cleanup(func() { curMon.Load().Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pathB := writeTestConfig(t, strings.Replace(baseConfig, `name = "first"`, `name = "second"`, 1))
	reloadConfig(ctx, handler, pathB)

	if got := handler.Config().Cluster.Name; got != "first" {
		t.Fatalf("cluster = %q, want first (canceled ctx must skip reload)", got)
	}
	if curMon.Load() != mon {
		t.Fatal("curMon replaced despite canceled ctx")
	}
}

func TestReloadConfigSkipsWhenReloadInProgress(t *testing.T) {
	path := writeTestConfig(t, baseConfig)
	handler, mon := newTestGateway(t, path)
	curMon.Store(mon)
	mon.Start()
	t.Cleanup(func() { curMon.Load().Stop() })

	pathB := writeTestConfig(t, strings.Replace(baseConfig, `name = "first"`, `name = "second"`, 1))
	reloadMu.Lock()
	defer reloadMu.Unlock()

	done := make(chan struct{})
	go func() {
		reloadConfig(context.Background(), handler, pathB)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reloadConfig blocked while another reload held the lock")
	}

	if got := handler.Config().Cluster.Name; got != "first" {
		t.Fatalf("cluster = %q, want first (skipped reload must not apply)", got)
	}
	if curMon.Load() != mon {
		t.Fatal("curMon replaced despite skipped reload")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestWarnListenChangesLogsWarningsForChangedAddresses(t *testing.T) {
	oldCfg := &config.Config{Gateway: config.GatewayConfig{Listen: ":8000", MetricsListen: ":9090"}}
	newCfg := &config.Config{Gateway: config.GatewayConfig{Listen: ":8001", MetricsListen: ":9091"}}

	out := captureStdout(t, func() { warnListenChanges(oldCfg, newCfg) })

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines %q, want 2 (one per changed address)", len(lines), out)
	}
	for i, line := range lines {
		var m map[string]string
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
		if m["level"] != "warn" {
			t.Errorf("line %d level = %q, want warn", i, m["level"])
		}
	}
}

func TestWarnListenChangesSilentWhenUnchanged(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{Listen: ":8000", MetricsListen: ":9090"}}

	out := captureStdout(t, func() { warnListenChanges(cfg, cfg) })

	if out != "" {
		t.Fatalf("warnListenChanges logged for unchanged addresses: %q", out)
	}
}
