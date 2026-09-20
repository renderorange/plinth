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
	status int
	header http.Header
	body   []byte
	err    error
}

type bufferedRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	err    error
}

func (b *bufferedRecorder) Header() http.Header {
	if b.header == nil {
		b.header = make(http.Header)
	}
	return b.header
}

func (b *bufferedRecorder) WriteHeader(code int) {
	if b.status != 0 {
		return
	}
	b.status = code
}

func (b *bufferedRecorder) Write(p []byte) (int, error) {
	return b.body.Write(p)
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

func proxyAttempt(r *http.Request, host string, port int, path string) (*attemptResult, error) {
	target, err := url.Parse(fmt.Sprintf("http://%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	r.URL.Path = path
	r.URL.RawPath = ""
	r.URL.RawQuery = ""

	buf := &bufferedRecorder{header: make(http.Header)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = proxyTransport
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		buf.err = err
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Body = &errCaptureBody{ReadCloser: resp.Body, onErr: func(err error) {
			buf.err = err
		}}
		return nil
	}
	proxy.ServeHTTP(buf, r)

	return &attemptResult{
		status: buf.status,
		header: buf.header,
		body:   buf.body.Bytes(),
		err:    buf.err,
	}, nil
}

func commitResponse(w http.ResponseWriter, res *attemptResult) {
	for k, vv := range res.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.status)
	_, _ = w.Write(res.body)
}
