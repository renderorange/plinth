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
	HealthCheckPanicsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "health_check_panics_total",
		Help: "Total health probe panics recovered by the monitor",
	})
)

func init() {
	prometheus.MustRegister(NodesHealthy)
	prometheus.MustRegister(NodesDegraded)
	prometheus.MustRegister(NodesDead)
	prometheus.MustRegister(RequestDuration)
	prometheus.MustRegister(RequestsTotal)
	prometheus.MustRegister(ProxyAttemptsTotal)
	prometheus.MustRegister(HealthCheckPanicsTotal)
}
