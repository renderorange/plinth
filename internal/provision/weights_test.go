package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"plinth/internal/config"
)

func TestWeightManagerPaths(t *testing.T) {
	wm := NewWeightManager("/var/lib/plinth/weights")

	if wm.weightsDir != "/var/lib/plinth/weights" {
		t.Errorf("weightsDir = %q, want /var/lib/plinth/weights", wm.weightsDir)
	}
}

func TestWeightManagerModelPath(t *testing.T) {
	wm := NewWeightManager("/var/lib/plinth/weights")

	path := wm.ModelPath("Qwen/Qwen2.5-7B-Instruct")
	want := "/var/lib/plinth/weights/Qwen/Qwen2.5-7B-Instruct"
	if path != want {
		t.Errorf("ModelPath() = %q, want %q", path, want)
	}
}

func TestValidateModelName(t *testing.T) {
	valid := []string{
		"Qwen/Qwen2.5-7B-Instruct",
		"Qwen2.5-7B-Instruct",
		"v0.1.2/mod-el-2",
		"a.b-c/d_e",
	}
	for _, name := range valid {
		if err := validateModelName(name); err != nil {
			t.Errorf("validateModelName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"../x",
		"a/../b",
		"/abs",
		"rel/",
		"a b",
		"a'b",
		`a"b`,
		"a$b",
		"a;b",
		"a|b",
		"a&b",
		"a`b",
		"a\nb",
		"a\\b",
	}
	for _, name := range invalid {
		if err := validateModelName(name); err == nil {
			t.Errorf("validateModelName(%q) = nil, want error", name)
		}
	}
}

func TestPullSkipsDownloadWhenWeightsExist(t *testing.T) {
	oldDownload := downloadWeights
	defer func() { downloadWeights = oldDownload }()

	called := false
	downloadWeights = func(_ context.Context, _, _ string) error {
		called = true
		return nil
	}

	weightsDir := t.TempDir()
	wm := NewWeightManager(weightsDir)
	modelPath := wm.ModelPath("Org/Model")
	if err := os.MkdirAll(modelPath, 0755); err != nil {
		t.Fatalf("creating existing weights dir: %v", err)
	}

	if err := wm.Pull(context.Background(), "Org/Model"); err != nil {
		t.Errorf("Pull() = %v, want nil", err)
	}
	if called {
		t.Error("download we ran despite weights already present")
	}
}

func TestPullAtomicOnFailure(t *testing.T) {
	oldDownload := downloadWeights
	defer func() { downloadWeights = oldDownload }()

	downloadErr := errors.New("simulated interrupted download")
	downloadWeights = func(_ context.Context, _, localDir string) error {
		if err := os.WriteFile(filepath.Join(localDir, "partial.safetensors"), []byte("chunk"), 0644); err != nil {
			t.Fatalf("writing partial file: %v", err)
		}
		return downloadErr
	}

	weightsDir := t.TempDir()
	wm := NewWeightManager(weightsDir)
	modelPath := wm.ModelPath("Org/Model")

	if err := wm.Pull(context.Background(), "Org/Model"); !errors.Is(err, downloadErr) {
		t.Errorf("Pull() = %v, want %v", err, downloadErr)
	}

	if _, err := os.Stat(modelPath); !os.IsNotExist(err) {
		t.Errorf("model dir exists after failed download (stat err = %v)", err)
	}

	entries, err := os.ReadDir(weightsDir)
	if err != nil {
		t.Fatalf("reading weights dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".partial-") {
			t.Errorf("partial download dir %q left behind after failure", entry.Name())
		}
		if !entry.IsDir() {
			t.Errorf("unexpected file %q in weights dir root after failure", entry.Name())
		}
	}

	secondRun := false
	downloadWeights = func(_ context.Context, _, localDir string) error {
		secondRun = true
		return os.WriteFile(filepath.Join(localDir, "model.safetensors"), []byte("done"), 0644)
	}
	if err := wm.Pull(context.Background(), "Org/Model"); err != nil {
		t.Errorf("Pull() after failed attempt = %v, want nil", err)
	}
	if !secondRun {
		t.Error("download did not re-run after failed attempt")
	}
}

func TestPullSuccess(t *testing.T) {
	oldDownload := downloadWeights
	defer func() { downloadWeights = oldDownload }()

	downloadWeights = func(_ context.Context, _, localDir string) error {
		return os.WriteFile(filepath.Join(localDir, "model.safetensors"), []byte("done"), 0644)
	}

	weightsDir := t.TempDir()
	wm := NewWeightManager(weightsDir)

	if err := wm.Pull(context.Background(), "Org/Model"); err != nil {
		t.Errorf("Pull() = %v, want nil", err)
	}

	if _, err := os.Stat(filepath.Join(wm.ModelPath("Org/Model"), "model.safetensors")); err != nil {
		t.Errorf("downloaded file missing: %v", err)
	}

	entries, err := os.ReadDir(weightsDir)
	if err != nil {
		t.Fatalf("reading weights dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("weights dir has %d entries, want exactly the model dir", len(entries))
	}
}

func TestPullRejectsInvalidModelName(t *testing.T) {
	wm := NewWeightManager(t.TempDir())
	if err := wm.Pull(context.Background(), "../evil"); err == nil {
		t.Error("Pull() with traversal name = nil, want error")
	}
}

func TestPushRejectsInvalidModelName(t *testing.T) {
	wm := NewWeightManager(t.TempDir())
	err := wm.Push(context.Background(), "a;rm -rf /", config.NodeConfig{}, SSHConfig{})
	if err == nil {
		t.Error("Push() with shell metacharacters = nil, want error")
	}
}
