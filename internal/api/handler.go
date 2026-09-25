package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
	"plinth/internal/log"
	"plinth/internal/metrics"
)

var maxBodyBytes int64 = 32 << 20

type Handler struct {
	state   atomic.Pointer[snapshot]
	bal     *balancer.Balancer
	mux     *http.ServeMux
	offline *offlineSet
}

func NewHandler(cfg *config.Config, mon *health.Monitor, bal *balancer.Balancer) *Handler {
	h := &Handler{
		bal:     bal,
		mux:     http.NewServeMux(),
		offline: newOfflineSet(),
	}
	h.state.Store(newSnapshot(cfg, mon))
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /v1/models", h.handleModels)
	h.mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)
	h.mux.HandleFunc("POST /v1/completions", h.handleCompletions)
	return h
}

// UpdateConfig swaps the serving configuration and monitor atomically.
// Requests already in flight keep the previous snapshot until they complete.
// The caller owns the superseded monitor and must stop it after this returns.
func (h *Handler) UpdateConfig(cfg *config.Config, mon *health.Monitor) {
	h.state.Store(newSnapshot(cfg, mon))
}

// Config returns the configuration currently serving requests.
func (h *Handler) Config() *config.Config {
	return h.state.Load().cfg
}

type offlineSet struct {
	mu    sync.Mutex
	until map[string]time.Time
}

func newOfflineSet() *offlineSet {
	return &offlineSet{until: make(map[string]time.Time)}
}

func (o *offlineSet) skip(ip string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	if t, ok := o.until[ip]; ok {
		if t.After(now) {
			return true
		}
		delete(o.until, ip)
	}
	return false
}

func (o *offlineSet) mark(ip string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if t, ok := o.until[ip]; ok && t.After(time.Now()) {
		return false
	}
	o.until[ip] = time.Now().Add(offlineTTL)
	return true
}

func (o *offlineSet) clear(ip string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.until, ip)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	states := h.state.Load().mon.GetNodeStates()
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
	for _, m := range h.state.Load().cfg.Models.Available {
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
	return h.state.Load().filterByRing(states, modelName)
}

func (h *Handler) proxyToVLLM(w http.ResponseWriter, r *http.Request, path string) {
	start := time.Now()
	st := h.state.Load()
	limit := st.cfg.Gateway.ResponseBufferLimit()

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
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	modelName := body.Model
	forwardRaw := raw
	if modelName == "" {
		if st.cfg.Models.Default == "" {
			http.Error(w, "model not specified and no default model is configured", http.StatusBadRequest)
			return
		}
		modelName = st.cfg.Models.Default
		forwardRaw, err = injectModel(raw, modelName)
		if err != nil {
			http.Error(w, "failed to apply default model to request body", http.StatusInternalServerError)
			return
		}
	}

	states := st.mon.GetNodeStates()
	filtered := st.filterByRing(states, modelName)
	if len(filtered) == 0 {
		http.Error(w, "no healthy node available", http.StatusServiceUnavailable)
		metrics.RequestDuration.Observe(time.Since(start).Seconds())
		metrics.RequestsTotal.WithLabelValues(modelName, "503").Inc()
		return
	}

	tried := make(map[string]bool)
	for i := 0; i < len(filtered); i++ {
		candidates := make([]health.NodeState, 0, len(filtered))
		for _, s := range filtered {
			if tried[s.IP] || h.offline.skip(s.IP) {
				continue
			}
			candidates = append(candidates, s)
		}
		if len(candidates) == 0 {
			break
		}
		node, err := h.bal.Select(candidates)
		if err != nil {
			break
		}
		tried[node.IP] = true

		port, ok := st.portForNode(node.IP)
		if !ok {
			http.Error(w, "selected node not found in configuration", http.StatusInternalServerError)
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, "500").Inc()
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(forwardRaw))
		if len(forwardRaw) != len(raw) {
			r.ContentLength = int64(len(forwardRaw))
		}

		if body.Stream {
			gate := newCommitGate(w)
			err = streamAttempt(r, node.IP, port, path, gate)
			if err != nil {
				http.Error(w, "failed to parse proxy target URL", http.StatusInternalServerError)
				metrics.RequestDuration.Observe(time.Since(start).Seconds())
				metrics.RequestsTotal.WithLabelValues(modelName, "500").Inc()
				return
			}
			switch {
			case gate.err == nil && gate.writeErr != nil:
				// The client stopped reading after the response was committed.
				// The node delivered a valid response, so this is not a node
				// failure and must never be retried or replaced with a 502.
				log.Error("client write failed after commit", "node", node.IP, "error", gate.writeErr.Error())
				metrics.ClientWriteFailuresTotal.WithLabelValues(modelName).Inc()
				h.offline.clear(node.IP)
				metrics.RequestDuration.Observe(time.Since(start).Seconds())
				metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", gate.status)).Inc()
				metrics.ProxyAttemptsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", gate.status)).Inc()
				return
			case gate.err == nil:
				if !gate.committed {
					gate.commit()
				}
				h.offline.clear(node.IP)
				metrics.RequestDuration.Observe(time.Since(start).Seconds())
				metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", gate.status)).Inc()
				metrics.ProxyAttemptsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", gate.status)).Inc()
				return
			case gate.committed:
				log.Error("stream terminated by upstream failure", "node", node.IP, "error", gate.err.Error())
				metrics.ProxyAttemptsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", gate.status)).Inc()
				metrics.RequestDuration.Observe(time.Since(start).Seconds())
				metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", gate.status)).Inc()
				return
			default:
				if classifyProxyError(gate.err) == outcomeClientAborted {
					return
				}
				log.Info("retrying after streaming failure before first chunk", "node", node.IP, "error", gate.err.Error())
				metrics.ProxyAttemptsTotal.WithLabelValues(modelName, "502").Inc()
				if h.offline.mark(node.IP) {
					st.mon.Recheck(node.IP)
				}
			}
			continue
		}

		res, err := proxyAttempt(r, node.IP, port, path, w, limit)
		if err != nil {
			http.Error(w, "failed to parse proxy target URL", http.StatusInternalServerError)
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, "500").Inc()
			return
		}

		if res.passthroughReason != "" && res.committed {
			log.Info("response exceeded buffer limit; passing through", "node", node.IP, "reason", res.passthroughReason, "limit", fmt.Sprintf("%d", limit))
			metrics.ResponsePassthroughTotal.WithLabelValues(modelName, res.passthroughReason).Inc()
		}

		if res.committed {
			status := res.status
			if status == 0 {
				status = http.StatusBadGateway
			}
			switch {
			case res.err != nil:
				log.Error("response truncated after commit", "node", node.IP, "error", res.err.Error())
			case res.writeErr != nil:
				// The client stopped reading after the response was committed.
				// The node delivered a valid response, so this is not a node
				// failure and must not be retried or replaced with a 502.
				log.Error("client write failed after commit", "node", node.IP, "error", res.writeErr.Error())
				metrics.ClientWriteFailuresTotal.WithLabelValues(modelName).Inc()
				h.offline.clear(node.IP)
			default:
				h.offline.clear(node.IP)
			}
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", status)).Inc()
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", status)).Inc()
			return
		}

		switch classifyProxyError(res.err) {
		case outcomeOK:
			h.offline.clear(node.IP)
			if err := commitResponse(w, res); err != nil {
				// The client stopped reading after the response was committed.
				// The node delivered a valid response, so this is not a node
				// failure and must not be retried or replaced with a 502.
				log.Error("client write failed after commit", "node", node.IP, "error", err.Error())
				metrics.ClientWriteFailuresTotal.WithLabelValues(modelName).Inc()
			}
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", res.status)).Inc()
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", res.status)).Inc()
			return
		case outcomeRetry:
			log.Info("retrying after connection failure", "node", node.IP, "error", res.err.Error())
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, "502").Inc()
			if h.offline.mark(node.IP) {
				st.mon.Recheck(node.IP)
			}
		case outcomeFail:
			log.Error("node response truncated or connection lost", "node", node.IP, "error", res.err.Error())
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, "502").Inc()
			http.Error(w, "upstream node error", http.StatusBadGateway)
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, "502").Inc()
			if isWriteOpError(res.err) {
				if h.offline.mark(node.IP) {
					st.mon.Recheck(node.IP)
				}
			}
			return
		case outcomeClientAborted:
			return
		}
	}

	http.Error(w, "no reachable node available", http.StatusServiceUnavailable)
	metrics.RequestDuration.Observe(time.Since(start).Seconds())
	metrics.RequestsTotal.WithLabelValues(modelName, "503").Inc()
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
