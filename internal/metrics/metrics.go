package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	NodesHealthy = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cluster_nodes_healthy",
		Help: "Number of healthy nodes",
	})
	NodesDegraded = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cluster_nodes_degraded",
		Help: "Number of degraded nodes",
	})
	NodesDead = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cluster_nodes_dead",
		Help: "Number of dead nodes",
	})
	RequestDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "gateway_request_duration_seconds",
		Help:    "Request duration in seconds",
		Buckets: prometheus.DefBuckets,
	})
	RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_requests_total",
		Help: "Total requests by model and status",
	}, []string{"model", "status"})
	ProxyAttemptsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_proxy_attempts_total",
		Help: "Proxy attempts by model and observed status",
	}, []string{"model", "status"})
	ResponsePassthroughTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_response_passthrough_total",
		Help: "Responses committed early and passed through to the client",
	}, []string{"model", "reason"})
	ClientWriteFailuresTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_response_client_write_failures_total",
		Help: "Committed responses whose write to the client failed",
	}, []string{"model"})
	HealthCheckPanicsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "health_check_panics_total",
		Help: "Total health probe panics recovered by the monitor",
	})
	ModelFilterExclusionsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_model_filter_exclusions_total",
		Help: "Nodes dropped from the candidate pool by the model filter",
	}, []string{"model", "reason"})
	NodeModelsOK = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gateway_node_models_ok",
		Help: "1 when node model discovery is settled (known or known_empty)",
	}, []string{"node"})
	ModelDiscoveryTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_model_discovery_total",
		Help: "Model discovery fetch results by node",
	}, []string{"node", "result"})
)

func init() {
	prometheus.MustRegister(NodesHealthy)
	prometheus.MustRegister(NodesDegraded)
	prometheus.MustRegister(NodesDead)
	prometheus.MustRegister(RequestDuration)
	prometheus.MustRegister(RequestsTotal)
	prometheus.MustRegister(ProxyAttemptsTotal)
	prometheus.MustRegister(ResponsePassthroughTotal)
	prometheus.MustRegister(ClientWriteFailuresTotal)
	prometheus.MustRegister(HealthCheckPanicsTotal)
	prometheus.MustRegister(ModelFilterExclusionsTotal)
	prometheus.MustRegister(NodeModelsOK)
	prometheus.MustRegister(ModelDiscoveryTotal)
}
