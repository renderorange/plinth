package api

import (
	"net/http"
)

type commitGate struct {
	rw        http.ResponseWriter
	header    http.Header
	status    int
	committed bool
	err       error
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
	return g.rw.Write(p)
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
