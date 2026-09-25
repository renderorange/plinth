package api

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	"time"
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
		{"response-header timeout retries", deadlineClaimingTimeout{}, outcomeRetry},
		{"deadline claiming timeout wrapped retries", &url.Error{Op: "Get", Err: deadlineClaimingTimeout{}}, outcomeRetry},
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

// deadlineClaimingTimeout mirrors net/http's unexported *timeoutError, returned
// as "net/http: timeout awaiting response headers" when ResponseHeaderTimeout
// fires. It satisfies net.Error and reports
// errors.Is(err, context.DeadlineExceeded) via Is, but exposes no Unwrap chain,
// so it is distinguishable from a genuine context.DeadlineExceeded only by
// walking the unwrap chain rather than by errors.Is.
type deadlineClaimingTimeout struct{}

func (deadlineClaimingTimeout) Error() string   { return "net/http: timeout awaiting response headers" }
func (deadlineClaimingTimeout) Timeout() bool   { return true }
func (deadlineClaimingTimeout) Temporary() bool { return true }
func (deadlineClaimingTimeout) Is(err error) bool {
	return err == context.DeadlineExceeded
}

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
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", httptest.NewRecorder(), 0)
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
	res, err := proxyAttempt(req, "127.0.0.1", 1, "/v1/completions", httptest.NewRecorder(), 0)
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

// TestProxyAttemptResponseHeaderTimeoutRetries reproduces the real net/http
// error returned when ResponseHeaderTimeout fires: *http.timeoutError, whose
// Is method reports errors.Is(err, context.DeadlineExceeded). It must be
// classified as a retryable node-side timeout, not a client abort.
func TestProxyAttemptResponseHeaderTimeoutRetries(t *testing.T) {
	old := proxyTransport.ResponseHeaderTimeout
	proxyTransport.ResponseHeaderTimeout = 100 * time.Millisecond
	defer func() { proxyTransport.ResponseHeaderTimeout = old }()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", httptest.NewRecorder(), 0)
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if res.err == nil {
		t.Fatal("expected response-header timeout error")
	}
	if !strings.Contains(res.err.Error(), "timeout awaiting response headers") {
		t.Fatalf("res.err = %v, want net/http response-header timeout", res.err)
	}
	if got := classifyProxyError(res.err); got != outcomeRetry {
		t.Errorf("classify = %v, want outcomeRetry (response-header timeout must be retried)", got)
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
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", httptest.NewRecorder(), 0)
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
	if _, err := proxyAttempt(req, "bad host", 80, "/v1/completions", httptest.NewRecorder(), 0); err == nil {
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
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/chat/completions", httptest.NewRecorder(), 0)
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

func TestCommitResponseZeroStatusDefaultsTo502(t *testing.T) {
	res := &attemptResult{
		status: 0,
		header: make(http.Header),
		body:   []byte(`{"error":"upstream"}`),
	}
	rec := httptest.NewRecorder()
	commitResponse(rec, res)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d (zero status must not reach WriteHeader)", rec.Code, http.StatusBadGateway)
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
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/chat/completions", httptest.NewRecorder(), 0)
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

	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/chat/completions", httptest.NewRecorder(), 0)
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
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", httptest.NewRecorder(), 0)
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

func TestAttemptRecorderBuffersUnderLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	a := newAttemptRecorder(rec, 16)
	a.Header().Set("Content-Type", "application/json")
	a.WriteHeader(200)
	if _, err := a.Write([]byte("hello")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("client received %q before commit", rec.Body.String())
	}
	if a.buf.String() != "hello" {
		t.Errorf("buffer = %q, want %q", a.buf.String(), "hello")
	}
	if a.committed {
		t.Error("committed = true, want false")
	}
	if a.passthroughReason != "" {
		t.Errorf("passthroughReason = %q, want empty", a.passthroughReason)
	}
}

func TestAttemptRecorderOverflowCommitsThenPassesThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	a := newAttemptRecorder(rec, 8)
	a.Header().Set("Content-Type", "application/json")
	a.WriteHeader(200)
	if _, err := a.Write([]byte("12345")); err != nil {
		t.Fatalf("first Write error: %v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("client received %q before overflow", rec.Body.String())
	}
	if _, err := a.Write([]byte("67890")); err != nil {
		t.Fatalf("second Write error: %v", err)
	}
	if !a.committed {
		t.Fatal("committed = false, want true after overflow")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "1234567890" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "1234567890")
	}
	if a.passthroughReason != "size_limit" {
		t.Errorf("passthroughReason = %q, want %q", a.passthroughReason, "size_limit")
	}
	if a.buf.Len() != 0 {
		t.Errorf("buffer not cleared after commit: %q", a.buf.String())
	}
}

func TestAttemptRecorderPassThroughDoesNotBuffer(t *testing.T) {
	rec := httptest.NewRecorder()
	a := newAttemptRecorder(rec, 8)
	a.passThrough = true
	a.passthroughReason = "content_length"
	a.WriteHeader(200)
	if _, err := a.Write([]byte("1234567890")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if rec.Body.String() != "1234567890" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "1234567890")
	}
	if a.buf.Len() != 0 {
		t.Errorf("pass-through must not buffer, got %q", a.buf.String())
	}
	if !a.committed {
		t.Error("committed = false, want true")
	}
}

func TestAttemptRecorderUnlimitedNeverCommitsEarly(t *testing.T) {
	rec := httptest.NewRecorder()
	a := newAttemptRecorder(rec, 0)
	a.WriteHeader(200)
	if _, err := a.Write([]byte(strings.Repeat("z", 1024))); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if a.committed {
		t.Error("committed = true, want false with unlimited")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("client received %q, want nothing until commit", rec.Body.String())
	}
}

type failingWriter struct {
	http.ResponseWriter
	failAfter int
	writes    int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > f.failAfter {
		return 0, errors.New("client write failed")
	}
	return f.ResponseWriter.Write(p)
}

func TestAttemptRecorderCommitOrderingOnFailingClientWrite(t *testing.T) {
	w := &failingWriter{ResponseWriter: httptest.NewRecorder(), failAfter: 0}
	a := newAttemptRecorder(w, 10)
	a.WriteHeader(200)
	if _, err := a.Write([]byte("abcd")); err != nil {
		t.Fatalf("buffered Write error: %v", err)
	}
	if a.committed {
		t.Fatal("committed = true before overflow")
	}
	if _, err := a.Write(bytes.Repeat([]byte("x"), 20)); err == nil {
		t.Fatal("overflow Write error = nil, want client write failure")
	}
	if !a.committed {
		t.Error("committed = false after WriteHeader started the client response; the handler could retry or rewrite the response")
	}
	if a.err == nil {
		t.Fatal("attemptRecorder.err = nil, want the client write failure captured")
	}
	if a.err.Error() != "client write failed" {
		t.Errorf("attemptRecorder.err = %q, want %q", a.err.Error(), "client write failed")
	}
	if a.buf.Len() != 0 {
		t.Errorf("buffer not reset on error: %q", a.buf.String())
	}
}

// TestAttemptRecorderPassThroughTailWriteErrorIsCaptured covers the client
// write that happens after commit, while the remainder of an oversized response
// streams through. failAfter 1 lets commit's buffered flush succeed and fails
// the tail write, which must still land in attemptRecorder.err so the handler
// logs "response truncated after commit" instead of counting a success.
func TestAttemptRecorderPassThroughTailWriteErrorIsCaptured(t *testing.T) {
	w := &failingWriter{ResponseWriter: httptest.NewRecorder(), failAfter: 1}
	a := newAttemptRecorder(w, 10)
	a.WriteHeader(200)
	if _, err := a.Write([]byte("abcd")); err != nil {
		t.Fatalf("buffered Write error: %v", err)
	}
	if _, err := a.Write(bytes.Repeat([]byte("x"), 20)); err == nil {
		t.Fatal("pass-through tail Write error = nil, want client write failure")
	}
	if !a.committed {
		t.Fatal("committed = false after the buffered prefix was flushed to the client")
	}
	if a.err == nil {
		t.Fatal("attemptRecorder.err = nil, want the tail client write failure captured")
	}
	if a.err.Error() != "client write failed" {
		t.Errorf("attemptRecorder.err = %q, want %q", a.err.Error(), "client write failed")
	}
}

func TestProxyAttemptOverflowPassesThroughToClient(t *testing.T) {
	payload := strings.Repeat("x", 100)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", rec, 10)
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if res.err != nil {
		t.Fatalf("res.err = %v, want nil", res.err)
	}
	if !res.committed {
		t.Fatal("committed = false, want true")
	}
	if res.body != nil {
		t.Errorf("res.body = %q, want nil when committed", res.body)
	}
	if res.passthroughReason != "size_limit" {
		t.Errorf("passthroughReason = %q, want size_limit", res.passthroughReason)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != payload {
		t.Errorf("client body len = %d, want %d", rec.Body.Len(), len(payload))
	}
}

func TestProxyAttemptContentLengthFastPath(t *testing.T) {
	payload := strings.Repeat("y", 100)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, payload)
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", rec, 10)
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if !res.committed {
		t.Fatal("committed = false, want true")
	}
	if res.passthroughReason != "content_length" {
		t.Errorf("passthroughReason = %q, want content_length", res.passthroughReason)
	}
	if rec.Body.String() != payload {
		t.Errorf("client body len = %d, want %d", rec.Body.Len(), len(payload))
	}
}

func TestProxyAttemptTruncatedAfterCommitKeepsCommitted(t *testing.T) {
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
		if req, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
			io.Copy(io.Discard, req.Body)
			req.Body.Close()
		}
		io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: 100\r\n\r\n"+strings.Repeat("y", 20))
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	res, err := proxyAttempt(req, "127.0.0.1", port, "/v1/completions", rec, 10)
	if err != nil {
		t.Fatalf("proxyAttempt error: %v", err)
	}
	if !res.committed {
		t.Fatal("committed = false, want true (20 bytes overflowed limit 10)")
	}
	if res.err == nil {
		t.Fatal("res.err = nil, want truncated-body error")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("client body empty, want the committed bytes")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (already committed)", rec.Code)
	}
}
