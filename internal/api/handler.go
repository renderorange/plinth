package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"plinth/internal/balancer"
	"plinth/internal/config"
	"plinth/internal/health"
	"plinth/internal/log"
	"plinth/internal/metrics"
)

var maxBodyBytes int64 = 32 << 20

type Handler struct {
	cfg        *config.Config
	mon        *health.Monitor
	bal        *balancer.Balancer
	mux        *http.ServeMux
	rings      map[string]map[string]bool
	standalone map[string]bool
	offline    *offlineSet
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
		offline:    newOfflineSet(),
	}
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /v1/models", h.handleModels)
	h.mux.HandleFunc("POST /v1/chat/completions", h.handleChatCompletions)
	h.mux.HandleFunc("POST /v1/completions", h.handleCompletions)
	return h
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

func portForNode(cfg *config.Config, ip string) (int, bool) {
	for _, nc := range cfg.Nodes {
		if nc.IP == ip {
			return nc.VLLMPort, true
		}
	}
	return 0, false
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

		port, ok := portForNode(h.cfg, node.IP)
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
					h.mon.Recheck(node.IP)
				}
			}
			continue
		}

		res, err := proxyAttempt(r, node.IP, port, path)
		if err != nil {
			http.Error(w, "failed to parse proxy target URL", http.StatusInternalServerError)
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, "500").Inc()
			return
		}

		switch classifyProxyError(res.err) {
		case outcomeOK:
			h.offline.clear(node.IP)
			commitResponse(w, res)
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", res.status)).Inc()
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, fmt.Sprintf("%d", res.status)).Inc()
			return
		case outcomeRetry:
			log.Info("retrying after connection failure", "node", node.IP, "error", res.err.Error())
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, "502").Inc()
			if h.offline.mark(node.IP) {
				h.mon.Recheck(node.IP)
			}
		case outcomeFail:
			log.Error("node response truncated or connection lost", "node", node.IP, "error", res.err.Error())
			metrics.ProxyAttemptsTotal.WithLabelValues(modelName, "502").Inc()
			http.Error(w, "upstream node error", http.StatusBadGateway)
			metrics.RequestDuration.Observe(time.Since(start).Seconds())
			metrics.RequestsTotal.WithLabelValues(modelName, "502").Inc()
			if isWriteOpError(res.err) {
				if h.offline.mark(node.IP) {
					h.mon.Recheck(node.IP)
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
