package gpumetrics

import (
	"fmt"
	"net/http"
)

type Exporter struct {
	provider MetricsProvider
}

func NewExporter(provider MetricsProvider) *Exporter {
	return &Exporter{provider: provider}
}

func (e *Exporter) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", makeHandler(e.provider))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	return http.ListenAndServe(addr, mux)
}

func makeHandler(provider MetricsProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		metrics, err := provider.Collect()
		if err != nil {
			http.Error(w, fmt.Sprintf("collecting metrics: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, FormatPrometheus(metrics))
	}
}
