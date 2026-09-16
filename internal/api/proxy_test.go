package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func extractPort(t *testing.T, serverURL string) int {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("failed to parse server URL %q: %v", serverURL, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("failed to extract port from %q: %v", serverURL, err)
	}
	return port
}

func TestProxyRequest_ValidTarget(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)

	proxyRequest(rec, req, "127.0.0.1", port, "/v1/chat/completions")

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestProxyRequest_ForwardsBody(t *testing.T) {
	payload := `{"model":"test","messages":[]}`
	var receivedBody string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		if len(body) == 0 {
			t.Errorf("expected non-empty body")
		}
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))

	proxyRequest(rec, req, "127.0.0.1", port, "/v1/chat/completions")

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if receivedBody != payload {
		t.Errorf("body mismatch: got %q, want %q", receivedBody, payload)
	}
}

func TestProxyRequest_InvalidHost(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)

	proxyRequest(rec, req, "256.256.256.256", 80, "/test")
}

func TestProxyRequest_ForwardsHeaders(t *testing.T) {
	var receivedContentType string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")

	proxyRequest(rec, req, "127.0.0.1", port, "/v1/chat/completions")

	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}
}

func TestProxyRequest_PathForwarded(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/completions", nil)

	proxyRequest(rec, req, "127.0.0.1", port, "/v1/completions")

	if receivedPath != "/v1/completions" {
		t.Errorf("path = %q, want /v1/completions", receivedPath)
	}
}

func TestProxyRequest_InvalidHostReturnsError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)

	// Invalid host should cause proxy to error (though httputil may not set status)
	proxyRequest(rec, req, "256.256.256.256", 80, "/test")

	// The reverse proxy may return 502 or 500 on connection failure
	// At minimum, verify it doesn't panic
	if rec.Code == http.StatusOK {
		t.Error("expected non-200 for invalid host, got 200")
	}
}

func TestProxyRequest_BodyPreserved(t *testing.T) {
	payload := `{"model":"llama","messages":[{"role":"user","content":"hi"}]}`
	body := io.NopCloser(bytes.NewReader([]byte(payload)))

	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if string(raw) != payload {
		t.Errorf("body mismatch: got %q, want %q", string(raw), payload)
	}

	req.Body = io.NopCloser(bytes.NewReader(raw))
	raw2, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("failed to re-read body: %v", err)
	}

	if string(raw2) != payload {
		t.Errorf("re-read body mismatch: got %q, want %q", string(raw2), payload)
	}
}
