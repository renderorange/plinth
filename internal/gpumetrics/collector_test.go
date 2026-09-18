package gpumetrics

import (
	"fmt"
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
		metrics []GPUMetrics
		want    string
	}{
		{
			name: "standard values",
			metrics: []GPUMetrics{
				{
					GPUUUID:     "GPU-123",
					MemoryUsed:  536870912,
					MemoryTotal: 12884901888,
					Utilization: 45,
					Temperature: 62,
				},
			},
			want: `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_memory_used_bytes{gpu="GPU-123"} 536870912
gpu_memory_total_bytes{gpu="GPU-123"} 12884901888
gpu_utilization_percent{gpu="GPU-123"} 45
gpu_temperature_celsius{gpu="GPU-123"} 62
`,
		},
		{
			name:    "zero values",
			metrics: []GPUMetrics{{GPUUUID: "GPU-zero"}},
			want: `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_memory_used_bytes{gpu="GPU-zero"} 0
gpu_memory_total_bytes{gpu="GPU-zero"} 0
gpu_utilization_percent{gpu="GPU-zero"} 0
gpu_temperature_celsius{gpu="GPU-zero"} 0
`,
		},
		{
			name:    "empty slice",
			metrics: []GPUMetrics{},
			want: `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
`,
		},
		{
			name: "multiple GPUs",
			metrics: []GPUMetrics{
				{GPUUUID: "GPU-aaa", MemoryUsed: 100, MemoryTotal: 200, Utilization: 50, Temperature: 60},
				{GPUUUID: "GPU-bbb", MemoryUsed: 300, MemoryTotal: 400, Utilization: 70, Temperature: 80},
			},
			want: `# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_memory_used_bytes{gpu="GPU-aaa"} 100
gpu_memory_total_bytes{gpu="GPU-aaa"} 200
gpu_utilization_percent{gpu="GPU-aaa"} 50
gpu_temperature_celsius{gpu="GPU-aaa"} 60
gpu_memory_used_bytes{gpu="GPU-bbb"} 300
gpu_memory_total_bytes{gpu="GPU-bbb"} 400
gpu_utilization_percent{gpu="GPU-bbb"} 70
gpu_temperature_celsius{gpu="GPU-bbb"} 80
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

func TestCollectorCollectDelegates(t *testing.T) {
	want := []GPUMetrics{
		{
			GPUUUID:     "GPU-test",
			MemoryUsed:  1024,
			MemoryTotal: 2048,
			Utilization: 50,
			Temperature: 40,
		},
	}
	c := NewCollector(func() ([]GPUMetrics, error) {
		return want, nil
	})

	got, err := c.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("Collect() returned %d items, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("Collect() = %+v, want %+v", got[0], want[0])
	}
}

func TestCollectorCollectError(t *testing.T) {
	c := NewCollector(func() ([]GPUMetrics, error) {
		return nil, fmt.Errorf("nvml error")
	})

	_, err := c.Collect()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "nvml error" {
		t.Errorf("error = %q, want %q", err.Error(), "nvml error")
	}
}

func TestCollectMultipleGPUs(t *testing.T) {
	fn := CollectFunc(func() ([]GPUMetrics, error) {
		return []GPUMetrics{
			{GPUUUID: "GPU-aaa", MemoryUsed: 100, MemoryTotal: 200, Utilization: 50, Temperature: 60},
			{GPUUUID: "GPU-bbb", MemoryUsed: 300, MemoryTotal: 400, Utilization: 70, Temperature: 80},
		}, nil
	})
	c := NewCollector(fn)
	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 GPUs, got %d", len(metrics))
	}
	if metrics[0].GPUUUID != "GPU-aaa" {
		t.Errorf("expected GPU-aaa, got %s", metrics[0].GPUUUID)
	}
	if metrics[1].GPUUUID != "GPU-bbb" {
		t.Errorf("expected GPU-bbb, got %s", metrics[1].GPUUUID)
	}
}

func TestCollectSingleGPU(t *testing.T) {
	fn := CollectFunc(func() ([]GPUMetrics, error) {
		return []GPUMetrics{
			{GPUUUID: "GPU-ccc", MemoryUsed: 500, MemoryTotal: 600, Utilization: 90, Temperature: 75},
		}, nil
	})
	c := NewCollector(fn)
	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU, got %d", len(metrics))
	}
	if metrics[0].GPUUUID != "GPU-ccc" {
		t.Errorf("expected GPU-ccc, got %s", metrics[0].GPUUUID)
	}
}

func TestCollectEmptySlice(t *testing.T) {
	fn := CollectFunc(func() ([]GPUMetrics, error) {
		return []GPUMetrics{}, nil
	})
	c := NewCollector(fn)
	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) != 0 {
		t.Fatalf("expected 0 GPUs, got %d", len(metrics))
	}
}
