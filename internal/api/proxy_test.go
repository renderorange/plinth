package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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

func TestClassifyProxyError(t *testing.T) {
	timeoutErr := &timeoutError{} // Timeout() true
	tests := []struct {
		name string
		err  error
		want attemptOutcome
	}{
		{"nil is OK", nil, outcomeOK},
		{"dial refused retries", &url.Error{Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}, outcomeRetry},
		{"dial timeout retries", &url.Error{Err: &net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr}}, outcomeRetry},
		{"non-dial timeout retries", timeoutErr, outcomeRetry},
		{"deadline exceeded is client aborted", context.DeadlineExceeded, outcomeClientAborted},
		{"canceled is client aborted", context.Canceled, outcomeClientAborted},
		{"read reset is fail", &url.Error{Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}, outcomeFail},
		{"write reset is fail", &url.Error{Err: &net.OpError{Op: "write", Net: "tcp", Err: syscall.ECONNRESET}}, outcomeFail},
		{"truncation is fail", io.ErrUnexpectedEOF, outcomeFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyProxyError(tt.err); got != tt.want {
				t.Errorf("classifyProxyError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "i/o timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

func TestIsWriteOpError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"write op error", &net.OpError{Op: "write", Net: "tcp", Err: syscall.ECONNRESET}, true},
		{"read op error", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, false},
		{"wrapped write op error", &url.Error{Op: "Post", Err: &net.OpError{Op: "write", Net: "tcp", Err: syscall.ECONNRESET}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWriteOpError(tt.err); got != tt.want {
				t.Errorf("isWriteOpError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestProxyAttemptSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{"prompt":"hi"}`))
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions")
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if res.err != nil {
		t.Fatalf("res.err = %v, want nil", res.err)
	}
	if res.status != http.StatusCreated {
		t.Errorf("status = %d, want 201", res.status)
	}
	if got := res.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if string(res.body) != `{"ok":true}` {
		t.Errorf("body = %q", res.body)
	}
}

func TestProxyAttemptDialRefused(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	res, err := proxyAttempt(req, "127.0.0.1", 1, "/v1/completions")
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if res.err == nil {
		t.Fatal("expected transport error for refused dial")
	}
	if classifyProxyError(res.err) != outcomeRetry {
		t.Errorf("classify = %v, want outcomeRetry", classifyProxyError(res.err))
	}
	if res.status != 0 {
		t.Errorf("status = %d, want 0 (nothing written on failure)", res.status)
	}
}

func TestProxyAttemptTruncatedBodyIsFail(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{")
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions")
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if res.err == nil {
		t.Fatal("expected body read error for truncated response")
	}
	if classifyProxyError(res.err) != outcomeFail {
		t.Errorf("classify = %v, want outcomeFail (no retry after headers)", classifyProxyError(res.err))
	}
}

func TestProxyAttemptInvalidTargetHost(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	if _, err := proxyAttempt(req, "bad host", 80, "/v1/completions"); err == nil {
		t.Fatal("expected parse error for malformed host")
	}
}

func TestProxyAttemptRoundTrip(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/chat/completions")
	if err != nil || res.err != nil {
		t.Fatalf("attempt failed: proxyAttempt err=%v res.err=%v", err, res.err)
	}
	rec := httptest.NewRecorder()
	commitResponse(rec, res)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestProxyAttemptForwardsBody(t *testing.T) {
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

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload))
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/chat/completions")
	if err != nil || res.err != nil {
		t.Fatalf("attempt failed: proxyAttempt err=%v res.err=%v", err, res.err)
	}
	rec := httptest.NewRecorder()
	commitResponse(rec, res)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if receivedBody != payload {
		t.Errorf("body mismatch: got %q, want %q", receivedBody, payload)
	}
}

func TestProxyAttemptForwardsHeaders(t *testing.T) {
	var receivedContentType atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType.Store(r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")

	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/chat/completions")
	if err != nil || res.err != nil {
		t.Fatalf("attempt failed: proxyAttempt err=%v res.err=%v", err, res.err)
	}
	rec := httptest.NewRecorder()
	commitResponse(rec, res)

	if ct, ok := receivedContentType.Load().(string); !ok || ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType.Load())
	}
}

func TestProxyAttemptPathForwarded(t *testing.T) {
	var receivedPath atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/completions", nil)
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions")
	if err != nil || res.err != nil {
		t.Fatalf("attempt failed: proxyAttempt err=%v res.err=%v", err, res.err)
	}
	rec := httptest.NewRecorder()
	commitResponse(rec, res)

	if p, ok := receivedPath.Load().(string); !ok || p != "/v1/completions" {
		t.Errorf("path = %q, want /v1/completions", receivedPath.Load())
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
