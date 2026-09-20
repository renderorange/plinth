package api

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommitGateCommitOnFirstWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Code = 0 // amendment: NewRecorder() defaults Code to 200; reset so "nothing committed" is observable
	g := newCommitGate(rec)

	g.Header().Set("Content-Type", "text/event-stream")
	g.WriteHeader(http.StatusOK)

	if rec.Code != 0 {
		t.Fatalf("recorder written before first body byte: code = %d", rec.Code)
	}

	if _, err := g.Write([]byte("data: hello\n\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Body.String(); got != "data: hello\n\n" {
		t.Errorf("body = %q", got)
	}
}

func TestCommitGateWritePassthroughAfterCommit(t *testing.T) {
	rec := httptest.NewRecorder()
	g := newCommitGate(rec)
	g.WriteHeader(http.StatusOK)

	if _, err := g.Write([]byte("one")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := g.Write([]byte("two")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := rec.Body.String(); got != "onetwo" {
		t.Errorf("body = %q", got)
	}
}

func TestCommitGateZeroStatusDefaultsTo502(t *testing.T) {
	rec := httptest.NewRecorder()
	g := newCommitGate(rec)
	if _, err := g.Write([]byte("boom")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.Code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", rec.Code)
	}
}

func TestCommitGateHeadersForwardedAfterCommit(t *testing.T) {
	rec := httptest.NewRecorder()
	g := newCommitGate(rec)
	g.Header().Set("Content-Type", "application/json")
	if _, err := g.Write([]byte("{}")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Simulate post-commit header mutations (trailer-style writes).
	g.Header().Add("X-Test", "late")
	if got := rec.Header().Get("X-Test"); got != "late" {
		t.Errorf("post-commit header mutation lost: X-Test = %q", got)
	}
}

func TestCommitGateFlushBeforeCommitIsNoop(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Code = 0 // amendment: NewRecorder() defaults Code to 200; reset so "nothing committed" is observable
	g := newCommitGate(rec)
	g.WriteHeader(http.StatusOK)
	g.Flush()
	if rec.Flushed {
		t.Error("pre-commit Flush must not reach the client")
	}
	if rec.Code != 0 {
		t.Errorf("code = %d, want 0 (nothing committed)", rec.Code)
	}
}

func TestCommitGateFlushAfterCommitDelegates(t *testing.T) {
	rec := httptest.NewRecorder()
	g := newCommitGate(rec)
	if _, err := g.Write([]byte("chunk")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	g.Flush()
	if !rec.Flushed {
		t.Error("post-commit Flush must flush the client writer")
	}
}

type errAfterReader struct {
	data string
	err  error
	done bool
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), nil
}

func (r *errAfterReader) Close() error { return nil }

func TestStreamBodyCapturesErrorAndReturnsEOF(t *testing.T) {
	boom := errors.New("connection reset")
	var captured error
	b := newStreamBody(&errAfterReader{data: "abc", err: boom}, func(err error) {
		captured = err
	})

	var out []byte
	buf := make([]byte, 8)
	for {
		n, err := b.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
	}

	if string(out) != "abc" {
		t.Errorf("data = %q, want %q", out, "abc")
	}
	if captured != boom {
		t.Errorf("captured = %v, want %v", captured, boom)
	}
}

type eofReader struct{}

func (eofReader) Read(p []byte) (int, error) { return 0, io.EOF }
func (eofReader) Close() error               { return nil }

func TestStreamBodyDoesNotCaptureEOF(t *testing.T) {
	captured := false
	b := newStreamBody(eofReader{}, func(error) { captured = true })
	buf := make([]byte, 8)
	_, err := b.Read(buf)
	if err != io.EOF {
		t.Fatalf("err = %v, want EOF", err)
	}
	if captured {
		t.Error("clean EOF must not be captured as an error")
	}
}

type blockingReadCloser struct {
	mu     sync.Mutex
	closed bool
	closeC chan struct{}
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{closeC: make(chan struct{})}
}

func (r *blockingReadCloser) Read(p []byte) (int, error) {
	<-r.closeC
	return 0, io.EOF
}

func (r *blockingReadCloser) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.closeC)
	}
	return nil
}

func TestStreamBodyDeadlineAbortsBlockedRead(t *testing.T) {
	old := streamFirstByteTimeout
	streamFirstByteTimeout = 20 * time.Millisecond
	defer func() { streamFirstByteTimeout = old }()

	fake := newBlockingReadCloser()
	var mu sync.Mutex
	var captured error
	b := newStreamBody(fake, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if captured == nil {
			captured = err
		}
	})

	readErrC := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		_, err := b.Read(buf)
		readErrC <- err
	}()

	select {
	case err := <-readErrC:
		if err != io.EOF {
			t.Fatalf("Read err = %v, want io.EOF after deadline", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending Read was not aborted by the deadline")
	}

	mu.Lock()
	defer mu.Unlock()
	if captured == nil {
		t.Fatal("no error captured on deadline")
	}
	if classifyProxyError(captured) != outcomeRetry {
		t.Errorf("classify = %v, want outcomeRetry (deadline must be retryable)", classifyProxyError(captured))
	}
}

func TestStreamBodyFirstByteDisarmsDeadline(t *testing.T) {
	old := streamFirstByteTimeout
	streamFirstByteTimeout = 20 * time.Millisecond
	defer func() { streamFirstByteTimeout = old }()

	fake := newDataThenBlockReadCloser()
	var captured error
	b := newStreamBody(fake, func(err error) { captured = err })

	buf := make([]byte, 8)
	if _, err := b.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	time.Sleep(3 * streamFirstByteTimeout)
	if captured != nil {
		t.Errorf("deadline fired after bytes arrived: %v", captured)
	}
	// Amendment (post-review): the timer must also be observed to be disarmed —
	// if onDeadline had fired it would have Close()d the fake. Without this
	// check the test passes even with the disarm logic deleted.
	fake.mu.Lock()
	closed := fake.closed
	fake.mu.Unlock()
	if closed {
		t.Fatal("deadline fired and closed the reader despite the first byte arriving")
	}
}

// Amendment: the plan reused blockingReadCloser here, but that fake can never
// return data, so the first Read can only end in deadline EOF — structurally
// impossible to pass. A data-then-block fake restores the test's intent:
// first byte disarms the timer; waiting far past the deadline captures nothing.
type dataThenBlockReadCloser struct {
	mu     sync.Mutex
	gave   bool
	closed bool
	closeC chan struct{}
}

func newDataThenBlockReadCloser() *dataThenBlockReadCloser {
	return &dataThenBlockReadCloser{closeC: make(chan struct{})}
}

func (r *dataThenBlockReadCloser) Read(p []byte) (int, error) {
	r.mu.Lock()
	if !r.gave {
		r.gave = true
		r.mu.Unlock()
		p[0] = 'x'
		return 1, nil
	}
	r.mu.Unlock()
	<-r.closeC
	return 0, io.EOF
}

func (r *dataThenBlockReadCloser) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.closeC)
	}
	return nil
}

func TestStreamBodyWorksWithHTTPBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "chunk\n")
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var captured error
	b := newStreamBody(resp.Body, func(err error) { captured = err })
	readAll(t, b)
	if captured != nil {
		t.Errorf("unexpected capture: %v", captured)
	}
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(data)
}

func sseServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	return srv
}

func TestStreamAttemptPassesThroughToGate(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: one\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	port := extractPort(t, backend.URL)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test/model","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	gate := newCommitGate(rec)

	if err := streamAttempt(req, "127.0.0.1", port, "/v1/chat/completions", gate); err != nil {
		t.Fatalf("streamAttempt: %v", err)
	}
	if gate.err != nil {
		t.Fatalf("gate.err = %v, want nil", gate.err)
	}
	if !gate.committed {
		t.Fatal("gate not committed for body response")
	}
	if gate.status != http.StatusOK {
		t.Errorf("gate.status = %d, want 200", gate.status)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("recorder code = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Errorf("body = %q, missing DONE", rec.Body.String())
	}
	if !rec.Flushed {
		t.Error("streamed response was never flushed")
	}
}

func TestStreamAttemptDialRefused(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	rec.Code = 0 // amendment: NewRecorder() defaults Code to 200; reset so "nothing committed" is observable
	gate := newCommitGate(rec)

	if err := streamAttempt(req, "127.0.0.1", 1, "/v1/completions", gate); err != nil {
		t.Fatalf("streamAttempt: %v", err)
	}
	if gate.err == nil {
		t.Fatal("expected transport error for refused dial")
	}
	if gate.committed {
		t.Fatal("gate committed on dial failure")
	}
	if rec.Code != 0 {
		t.Errorf("recorder code = %d, want 0 (nothing written)", rec.Code)
	}
}

// Amendment B (post-run): the original plan backend wrote a data chunk then
// reset, but that races ACK-vs-RST on loopback: on the CI-pinned go1.22.12
// the reset sometimes surfaces as clean EOF after data delivery, so gate.err
// was nil in a subset of count=10 runs. A headers-only-then-RST backend has
// no data to race: the body read must fail before any byte is delivered,
// making "reset before commit leaves the gate clean" deterministic. Renamed
// accordingly (the committed-truncated case is T6's handler-level concern).
func TestStreamAttemptCapturesResetBeforeCommit(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n")
			conn.(*net.TCPConn).SetLinger(0)
			conn.Close()
		}
	}()

	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	rec.Code = 0 // amendment: httptest recorder defaults Code to 200
	gate := newCommitGate(rec)

	port := ln.Addr().(*net.TCPAddr).Port
	if err := streamAttempt(req, "127.0.0.1", port, "/v1/completions", gate); err != nil {
		t.Fatalf("streamAttempt: %v", err)
	}
	if gate.err == nil {
		t.Fatal("expected transport error from reset stream")
	}
	if gate.committed {
		t.Fatal("gate committed on reset before first chunk")
	}
	if rec.Code != 0 {
		t.Errorf("recorder code = %d, want 0 (nothing written)", rec.Code)
	}
}

func TestStreamAttemptInvalidTargetHost(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	gate := newCommitGate(rec)
	if err := streamAttempt(req, "bad host", 80, "/v1/completions", gate); err == nil {
		t.Fatal("expected parse error for malformed host")
	}
}
