package gpumetrics

import (
	"fmt"
	"io"
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
			wantCode:   500,
			wantNotIn:  []string{"gpu_memory_used_bytes"},
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
