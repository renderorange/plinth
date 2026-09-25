package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const (
	dialTimeout           = 5 * time.Second
	responseHeaderTimeout = 60 * time.Second
	offlineTTL            = 10 * time.Second
)

var proxyTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ResponseHeaderTimeout: responseHeaderTimeout,
	ForceAttemptHTTP2:     false,
	MaxIdleConnsPerHost:   4,
	IdleConnTimeout:       90 * time.Second,
}

type attemptResult struct {
	status            int
	header            http.Header
	body              []byte
	err               error
	committed         bool
	passthroughReason string
}

// attemptRecorder buffers an upstream response until it completes or exceeds
// limit bytes. On overflow it commits the buffered prefix to w and passes the
// remainder through. limit <= 0 disables the bound. Once committed, every byte
// goes to w and the attempt may not be retried or replaced with a 502.
type attemptRecorder struct {
	w                 http.ResponseWriter
	limit             int64
	header            http.Header
	status            int
	err               error
	buf               bytes.Buffer
	committed         bool
	passThrough       bool
	passthroughReason string
}

func newAttemptRecorder(w http.ResponseWriter, limit int64) *attemptRecorder {
	return &attemptRecorder{w: w, limit: limit, header: make(http.Header)}
}

func (a *attemptRecorder) Header() http.Header {
	if a.header == nil {
		a.header = make(http.Header)
	}
	return a.header
}

func (a *attemptRecorder) WriteHeader(code int) {
	if a.status != 0 {
		return
	}
	a.status = code
}

func (a *attemptRecorder) Write(p []byte) (int, error) {
	if a.committed || a.passThrough {
		return a.writeThrough(p)
	}
	if a.limit > 0 && int64(a.buf.Len())+int64(len(p)) > a.limit {
		if a.passthroughReason == "" {
			a.passthroughReason = "size_limit"
		}
		if err := a.commit(); err != nil {
			return 0, err
		}
		return a.writeThrough(p)
	}
	return a.buf.Write(p)
}

func (a *attemptRecorder) writeThrough(p []byte) (int, error) {
	if !a.committed {
		if err := a.commit(); err != nil {
			return 0, err
		}
	}
	return a.w.Write(p)
}

func (a *attemptRecorder) commit() error {
	if a.committed {
		return nil
	}
	dst := a.w.Header()
	for k, vv := range a.header {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
	status := a.status
	if status == 0 {
		status = http.StatusBadGateway
	}
	a.w.WriteHeader(status)
	if a.buf.Len() > 0 {
		if _, err := a.w.Write(a.buf.Bytes()); err != nil {
			return err
		}
		a.buf.Reset()
	}
	a.committed = true
	return nil
}

// Flush forwards to the client only after commit; while buffering there is
// nothing to flush.
func (a *attemptRecorder) Flush() {
	if !a.committed {
		return
	}
	if f, ok := a.w.(http.Flusher); ok {
		f.Flush()
	}
}

type errCaptureBody struct {
	io.ReadCloser
	onErr func(error)
}

func (e *errCaptureBody) Read(p []byte) (int, error) {
	n, err := e.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		e.onErr(err)
	}
	return n, err
}

type attemptOutcome int

const (
	outcomeOK attemptOutcome = iota
	outcomeRetry
	outcomeFail
	outcomeClientAborted
)

func classifyProxyError(err error) attemptOutcome {
	if err == nil {
		return outcomeOK
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return outcomeClientAborted
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			return outcomeRetry
		}
		return outcomeFail
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return outcomeRetry
	}
	return outcomeFail
}

// isWriteOpError reports whether err is a network write-phase failure, i.e. a
// stale keep-alive connection reset while uploading the request. Such errors
// strongly suggest a dead node; read-side errors do not.
func isWriteOpError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "write"
}

func proxyAttempt(r *http.Request, host string, port int, path string, w http.ResponseWriter, limit int64) (*attemptResult, error) {
	proxy, err := newProxy(host, port, path, r)
	if err != nil {
		return nil, err
	}

	rec := newAttemptRecorder(w, limit)
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		rec.err = err
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if rec.limit > 0 && resp.ContentLength > rec.limit {
			rec.passThrough = true
			rec.passthroughReason = "content_length"
		}
		resp.Body = &errCaptureBody{ReadCloser: resp.Body, onErr: func(err error) {
			rec.err = err
		}}
		return nil
	}
	proxy.ServeHTTP(rec, r)

	res := &attemptResult{
		status:            rec.status,
		header:            rec.header,
		body:              rec.buf.Bytes(),
		err:               rec.err,
		committed:         rec.committed,
		passthroughReason: rec.passthroughReason,
	}
	if rec.committed {
		res.body = nil
	}
	return res, nil
}

func newProxy(host string, port int, path string, r *http.Request) (*httputil.ReverseProxy, error) {
	target, err := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	r.URL.Path = path
	r.URL.RawPath = ""
	r.URL.RawQuery = ""

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = proxyTransport
	proxy.FlushInterval = -1
	return proxy, nil
}

func commitResponse(w http.ResponseWriter, res *attemptResult) {
	for k, vv := range res.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	status := res.status
	if status == 0 {
		status = http.StatusBadGateway
	}
	w.WriteHeader(status)
	_, _ = w.Write(res.body)
}
