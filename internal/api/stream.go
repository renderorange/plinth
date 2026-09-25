package api

import (
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

type commitGate struct {
	rw        http.ResponseWriter
	header    http.Header
	status    int
	committed bool
	err       error
	// writeErr holds a client-side failure of the response write, as opposed
	// to err, which carries upstream transport and body-read failures. The two
	// must not share a channel: the handler reports err as an upstream problem
	// (and can mark the node offline before the first chunk), while a client
	// that stopped reading is not a node failure.
	writeErr error
}

func newCommitGate(rw http.ResponseWriter) *commitGate {
	return &commitGate{rw: rw, header: make(http.Header)}
}

func (g *commitGate) Header() http.Header {
	if g.committed {
		return g.rw.Header()
	}
	return g.header
}

func (g *commitGate) WriteHeader(code int) {
	if g.status != 0 {
		return
	}
	g.status = code
}

func (g *commitGate) Write(p []byte) (int, error) {
	if !g.committed {
		g.commit()
	}
	n, err := g.rw.Write(p)
	if err != nil {
		g.writeErr = err
	}
	return n, err
}

func (g *commitGate) commit() {
	g.committed = true
	for k, vv := range g.header {
		for _, v := range vv {
			g.rw.Header().Add(k, v)
		}
	}
	status := g.status
	if status == 0 {
		status = http.StatusBadGateway
	}
	g.rw.WriteHeader(status)
}

func (g *commitGate) Flush() {
	if !g.committed {
		return
	}
	_ = http.NewResponseController(g.rw).Flush()
}

var streamFirstByteTimeout = 10 * time.Second

type firstByteTimeout struct{}

// Amendment: net.Error requires Temporary(); without it the `var _ net.Error`
// assertion fails and classifyProxyError's errors.As (proxy.go:101-102) never
// matches, breaking the deadline-retry contract. Mirrors proxy_test.go's timeoutError.
func (firstByteTimeout) Error() string   { return "timed out waiting for first stream chunk" }
func (firstByteTimeout) Timeout() bool   { return true }
func (firstByteTimeout) Temporary() bool { return true }

var _ net.Error = firstByteTimeout{}

type streamBody struct {
	rc    io.ReadCloser
	onErr func(error)

	mu       sync.Mutex
	gotByte  bool
	timedOut bool
	timer    *time.Timer
}

func newStreamBody(rc io.ReadCloser, onErr func(error)) *streamBody {
	b := &streamBody{rc: rc, onErr: onErr}
	b.timer = time.AfterFunc(streamFirstByteTimeout, b.onDeadline)
	return b
}

func (b *streamBody) onDeadline() {
	b.mu.Lock()
	if b.gotByte {
		b.mu.Unlock()
		return
	}
	b.timedOut = true
	b.mu.Unlock()
	b.rc.Close()
}

func (b *streamBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.mu.Lock()
		if !b.gotByte {
			b.gotByte = true
			b.timer.Stop()
		}
		b.mu.Unlock()
		return n, nil
	}
	if err == nil {
		return 0, nil
	}
	b.timer.Stop()
	b.mu.Lock()
	timedOut := b.timedOut
	b.mu.Unlock()
	// Amendment: dropped `&& err != io.EOF`. The brief's own deadline test
	// returns clean EOF after Close(), and real transports can too — without
	// capturing the timeout on EOF the deadline error would be lost and the
	// caller would treat an uncommitted dead stream as a clean empty response.
	// After the deadline fires, ANY completion of the read is its consequence.
	if timedOut {
		b.onErr(firstByteTimeout{})
		return 0, io.EOF
	}
	if err != nil && err != io.EOF {
		b.onErr(err)
		return 0, io.EOF
	}
	return 0, io.EOF
}

// Amendment A: http.Response.Body is an io.ReadCloser; ReverseProxy defers
// res.Body.Close() on the replacement body, so streamBody must implement
// Close. Stops the deadline timer and closes the wrapped body.
func (b *streamBody) Close() error {
	b.timer.Stop()
	return b.rc.Close()
}

func streamAttempt(r *http.Request, host string, port int, path string, gate *commitGate) error {
	proxy, err := newProxy(host, port, path, r)
	if err != nil {
		return err
	}
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		gate.err = err
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Body = newStreamBody(resp.Body, func(err error) {
			gate.err = err
		})
		return nil
	}
	proxy.ServeHTTP(gate, r)
	return nil
}
