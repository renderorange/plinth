package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func captureOutput(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestInfoProducesJSON(t *testing.T) {
	out := captureOutput(func() {
		Info("test message", "key", "value")
	})

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if m["level"] != "info" {
		t.Errorf("level = %q, want %q", m["level"], "info")
	}
	if m["msg"] != "test message" {
		t.Errorf("msg = %q, want %q", m["msg"], "test message")
	}
	if m["key"] != "value" {
		t.Errorf("key = %q, want %q", m["key"], "value")
	}
	if m["timestamp"] == "" {
		t.Error("timestamp should not be empty")
	}
}

func TestErrorProducesJSON(t *testing.T) {
	out := captureOutput(func() {
		Error("something failed", "code", "500")
	})

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if m["level"] != "error" {
		t.Errorf("level = %q, want %q", m["level"], "error")
	}
	if m["msg"] != "something failed" {
		t.Errorf("msg = %q, want %q", m["msg"], "something failed")
	}
	if m["code"] != "500" {
		t.Errorf("code = %q, want %q", m["code"], "500")
	}
}

func TestInfoWithNoKeyValues(t *testing.T) {
	out := captureOutput(func() {
		Info("bare message")
	})

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if m["msg"] != "bare message" {
		t.Errorf("msg = %q, want %q", m["msg"], "bare message")
	}
	if m["level"] != "info" {
		t.Errorf("level = %q, want %q", m["level"], "info")
	}
}

func TestInfoWithOddKeyValues(t *testing.T) {
	out := captureOutput(func() {
		Info("odd args", "key1", "val1", "key2")
	})

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if m["key1"] != "val1" {
		t.Errorf("key1 = %q, want %q", m["key1"], "val1")
	}
	if _, ok := m["key2"]; ok {
		t.Error("key2 should not be present (odd arg)")
	}
}

func TestErrorWithNoKeyValues(t *testing.T) {
	out := captureOutput(func() {
		Error("bare error")
	})

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if m["level"] != "error" {
		t.Errorf("level = %q, want %q", m["level"], "error")
	}
	if m["msg"] != "bare error" {
		t.Errorf("msg = %q, want %q", m["msg"], "bare error")
	}
}

func TestInfoWithMultipleKeyValues(t *testing.T) {
	out := captureOutput(func() {
		Info("request", "method", "POST", "path", "/v1/chat", "status", "200")
	})

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if m["method"] != "POST" {
		t.Errorf("method = %q, want POST", m["method"])
	}
	if m["path"] != "/v1/chat" {
		t.Errorf("path = %q, want /v1/chat", m["path"])
	}
	if m["status"] != "200" {
		t.Errorf("status = %q, want 200", m["status"])
	}
}

func TestConcurrentLogging(t *testing.T) {
	out := captureOutput(func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				Info("concurrent", "goroutine", fmt.Sprintf("%d", n))
			}(i)
		}
		wg.Wait()
	})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 50 {
		t.Fatalf("expected 50 log lines, got %d", len(lines))
	}

	goroutines := make(map[string]bool)
	for i, line := range lines {
		var m map[string]string
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d: invalid JSON: %v\nline: %s", i, err, line)
		}
		if m["level"] != "info" {
			t.Errorf("line %d: level = %q, want info", i, m["level"])
		}
		if m["msg"] != "concurrent" {
			t.Errorf("line %d: msg = %q, want concurrent", i, m["msg"])
		}
		goroutines[m["goroutine"]] = true
	}

	if len(goroutines) != 50 {
		t.Errorf("expected 50 unique goroutine values, got %d", len(goroutines))
	}
}
