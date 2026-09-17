package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"distributed-vram/internal/balancer"
	"distributed-vram/internal/config"
	"distributed-vram/internal/health"
	"distributed-vram/internal/metrics"
)

var maxBodyBytes int64 = 32 << 20

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type Handler struct {
	cfg *config.Config
	mon *health.Monitor
	bal *balancer.Balancer
	mux *http.ServeMux
}

func NewHandler(cfg *config.Config, mon *health.Monitor, bal *balancer.Balancer) *Handler {
	h := &Handler{
		cfg: cfg,
		mon: mon,
		bal: bal,
		mux: http.NewServeMux(),
	}
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /v1/models", h.handleModels)
	h.mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)
	h.mux.HandleFunc("POST /v1/completions", h.handleCompletions)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	states := h.mon.GetNodeStates()
	healthy := 0
	for _, s := range states {
		if s.Status == health.Healthy {
			healthy++
		}
	}
	status := "ok"
	if healthy == 0 {
		status = "error"
	} else if healthy < len(states) {
		status = "degraded"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  status,
		"nodes":   len(states),
		"healthy": healthy,
	})
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	type modelEntry struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	type modelsResponse struct {
		Object string       `json:"object"`
		Data   []modelEntry `json:"data"`
	}

	var data []modelEntry
	for _, m := range h.cfg.Models.Available {
		data = append(data, modelEntry{
			ID:      m.Name,
			Object:  "model",
			OwnedBy: "cluster",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(modelsResponse{
		Object: "list",
		Data:   data,
	})
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.proxyToVLLM(w, r, "/v1/chat/completions")
}

func (h *Handler) handleCompletions(w http.ResponseWriter, r *http.Request) {
	h.proxyToVLLM(w, r, "/v1/completions")
}

func (h *Handler) proxyToVLLM(w http.ResponseWriter, r *http.Request, path string) {
	start := time.Now()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	states := h.mon.GetNodeStates()
	node, err := h.bal.Select(states)
	if err != nil {
		http.Error(w, fmt.Sprintf("no healthy node available: %v", err), http.StatusServiceUnavailable)
		metrics.RequestDuration.Observe(time.Since(start).Seconds())
		metrics.RequestsTotal.WithLabelValues(body.Model, "503").Inc()
		return
	}

	var port int
	found := false
	for _, nc := range h.cfg.Nodes {
		if nc.IP == node.IP {
			port = nc.VLLMPort
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "selected node not found in configuration", http.StatusInternalServerError)
		metrics.RequestDuration.Observe(time.Since(start).Seconds())
		metrics.RequestsTotal.WithLabelValues(body.Model, "500").Inc()
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(raw))
	sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	proxyRequest(sr, r, node.IP, port, path)

	metrics.RequestDuration.Observe(time.Since(start).Seconds())
	metrics.RequestsTotal.WithLabelValues(body.Model, fmt.Sprintf("%d", sr.status)).Inc()
}
