package gpumetrics

import (
	"testing"
)

func TestGPUMetricsFields(t *testing.T) {
	tests := []struct {
		name      string
		metrics   GPUMetrics
		wantUsed  uint64
		wantTotal uint64
		wantUtil  uint32
		wantTemp  uint32
	}{
		{
			name: "standard values",
			metrics: GPUMetrics{
				MemoryUsed:  1024 * 1024 * 512,
				MemoryTotal: 1024 * 1024 * 12288,
				Utilization: 45,
				Temperature: 62,
			},
			wantUsed:  536870912,
			wantTotal: 12884901888,
			wantUtil:  45,
			wantTemp:  62,
		},
		{
			name: "zero values",
			metrics: GPUMetrics{
				MemoryUsed:  0,
				MemoryTotal: 0,
				Utilization: 0,
				Temperature: 0,
			},
			wantUsed:  0,
			wantTotal: 0,
			wantUtil:  0,
			wantTemp:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.metrics.MemoryUsed != tt.wantUsed {
				t.Errorf("MemoryUsed = %d, want %d", tt.metrics.MemoryUsed, tt.wantUsed)
			}
			if tt.metrics.MemoryTotal != tt.wantTotal {
				t.Errorf("MemoryTotal = %d, want %d", tt.metrics.MemoryTotal, tt.wantTotal)
			}
			if tt.metrics.Utilization != tt.wantUtil {
				t.Errorf("Utilization = %d, want %d", tt.metrics.Utilization, tt.wantUtil)
			}
			if tt.metrics.Temperature != tt.wantTemp {
				t.Errorf("Temperature = %d, want %d", tt.metrics.Temperature, tt.wantTemp)
			}
		})
	}
}

func TestFormatPrometheus(t *testing.T) {
	tests := []struct {
		name    string
		metrics GPUMetrics
		want    string
	}{
		{
			name: "standard values",
			metrics: GPUMetrics{
				MemoryUsed:  536870912,
				MemoryTotal: 12884901888,
				Utilization: 45,
				Temperature: 62,
			},
			want: `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
gpu_memory_used_bytes 536870912
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
gpu_memory_total_bytes 12884901888
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
gpu_utilization_percent 45
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_temperature_celsius 62
`,
		},
		{
			name: "zero values",
			metrics: GPUMetrics{
				MemoryUsed:  0,
				MemoryTotal: 0,
				Utilization: 0,
				Temperature: 0,
			},
			want: `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
gpu_memory_used_bytes 0
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
gpu_memory_total_bytes 0
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
gpu_utilization_percent 0
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_temperature_celsius 0
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatPrometheus(tt.metrics)
			if got != tt.want {
				t.Errorf("FormatPrometheus() =\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}
