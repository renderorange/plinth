package gpumetrics

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExporterHandler(t *testing.T) {
	tests := []struct {
		name       string
		collectFn  func() (GPUMetrics, error)
		wantCode   int
		wantInBody []string
		wantNotIn  []string
	}{
		{
			name: "success returns metrics",
			collectFn: func() (GPUMetrics, error) {
				return GPUMetrics{
					MemoryUsed:  536870912,
					MemoryTotal: 12884901888,
					Utilization: 45,
					Temperature: 62,
				}, nil
			},
			wantCode: 200,
			wantInBody: []string{
				"gpu_memory_used_bytes",
				"gpu_utilization_percent",
				"gpu_memory_total_bytes",
				"gpu_temperature_celsius",
			},
		},
		{
			name: "collector error returns 500",
			collectFn: func() (GPUMetrics, error) {
				return GPUMetrics{}, fmt.Errorf("nvml error")
			},
			wantCode:  500,
			wantNotIn: []string{"gpu_memory_used_bytes"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := NewCollector(tt.collectFn)
			handler := makeHandler(collector)

			req := httptest.NewRequest("GET", "/metrics", nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}

			body, _ := io.ReadAll(w.Body)
			bodyStr := string(body)
			for _, s := range tt.wantInBody {
				if !strings.Contains(bodyStr, s) {
					t.Errorf("response missing %q", s)
				}
			}
			for _, s := range tt.wantNotIn {
				if strings.Contains(bodyStr, s) {
					t.Errorf("response should not contain %q", s)
				}
			}
		})
	}
}

func TestExporterHandlerContentType(t *testing.T) {
	collector := NewCollector(func() (GPUMetrics, error) {
		return GPUMetrics{MemoryUsed: 100}, nil
	})
	handler := makeHandler(collector)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; version=0.0.4")
	}
}

func TestExporterHealthEndpoint(t *testing.T) {
	collector := NewCollector(func() (GPUMetrics, error) {
		return GPUMetrics{}, nil
	})

	exporter := NewExporter(collector)
	srv := httptest.NewServer(exporter.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("failed to GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "ok") {
		t.Errorf("body = %q, want ok", string(body))
	}
}

func TestExporterHandlerIntegration(t *testing.T) {
	collector := NewCollector(func() (GPUMetrics, error) {
		return GPUMetrics{
			MemoryUsed:  1073741824,
			MemoryTotal: 8589934592,
			Utilization: 65,
			Temperature: 58,
		}, nil
	})

	exporter := NewExporter(collector)
	srv := httptest.NewServer(exporter.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	wantContains := []string{
		"gpu_memory_used_bytes 1073741824",
		"gpu_memory_total_bytes 8589934592",
		"gpu_utilization_percent 65",
		"gpu_temperature_celsius 58",
	}
	for _, s := range wantContains {
		if !strings.Contains(bodyStr, s) {
			t.Errorf("response missing %q", s)
		}
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; version=0.0.4")
	}

	resp2, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("failed to GET /health: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp2.StatusCode)
	}
}

func TestExporterHandlerIntegrationError(t *testing.T) {
	collector := NewCollector(func() (GPUMetrics, error) {
		return GPUMetrics{}, fmt.Errorf("nvml init failed")
	})

	exporter := NewExporter(collector)
	srv := httptest.NewServer(exporter.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("failed to GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}
