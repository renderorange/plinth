package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"plinth/internal/api"
	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
)

func TestMainCompiles(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gateway binary failed to compile: %v\n%s", err, out)
	}
}

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
