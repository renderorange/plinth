package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
