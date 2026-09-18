package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
	"plinth/internal/metrics"
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
	cfg        *config.Config
	mon        *health.Monitor
	bal        *balancer.Balancer
	mux        *http.ServeMux
	rings      map[string]map[string]bool
	standalone map[string]bool
}

func NewHandler(cfg *config.Config, mon *health.Monitor, bal *balancer.Balancer) *Handler {
	rings := make(map[string]map[string]bool)
	standalone := make(map[string]bool)
	for _, n := range cfg.Nodes {
		if n.Ring != "" {
			ips := rings[n.Ring]
			if ips == nil {
				ips = make(map[string]bool)
				rings[n.Ring] = ips
			}
			ips[n.IP] = true
		} else {
			standalone[n.IP] = true
		}
	}
	h := &Handler{
		cfg:        cfg,
		mon:        mon,
		bal:        bal,
		mux:        http.NewServeMux(),
		rings:      rings,
		standalone: standalone,
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

func (h *Handler) filterByRing(states []health.NodeState, modelName string) []health.NodeState {
	pool := h.standalone
	if ring := h.cfg.ModelRing(modelName); ring != "" {
		pool = h.rings[ring]
	}
	var filtered []health.NodeState
	for _, s := range states {
		if pool[s.IP] {
			filtered = append(filtered, s)
		}
	}
	return filtered
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

	modelName := body.Model
	forwardRaw := raw
	if modelName == "" {
		if h.cfg.Models.Default == "" {
			http.Error(w, "model not specified and no default model is configured", http.StatusBadRequest)
			return
		}
		modelName = h.cfg.Models.Default
		forwardRaw, err = injectModel(raw, modelName)
		if err != nil {
			http.Error(w, "failed to apply default model to request body", http.StatusInternalServerError)
			return
		}
	}

	states := h.mon.GetNodeStates()
	filtered := h.filterByRing(states, modelName)
	node, err := h.bal.Select(filtered)
	if err != nil {
		http.Error(w, fmt.Sprintf("no healthy node available: %v", err), http.StatusServiceUnavailable)
		metrics.RequestDuration.Observe(time.Since(start).Seconds())
		metrics.RequestsTotal.WithLabelValues(modelName, "503").Inc()
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
		metrics.RequestsTotal.WithLabelValues(modelName, "500").Inc()
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(forwardRaw))
	if len(forwardRaw) != len(raw) {
		r.ContentLength = int64(len(forwardRaw))
	}
	sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	proxyRequest(sr, r, node.IP, port, path)

	metrics.RequestDuration.Observe(time.Since(start).Seconds())
	metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", sr.status)).Inc()
}

// injectModel adds the model to a JSON request body that omitted it, so the
// backend receives a complete OpenAI-compatible payload.
func injectModel(raw []byte, modelName string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	body["model"] = modelName
	return json.Marshal(body)
}
