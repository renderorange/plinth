package gpumetrics

import (
	"testing"
)

func TestNewNVMLCollectorNoGPU(t *testing.T) {
	collector, err := NewNVMLCollector()
	if err == nil {
		collector.Close()
		t.Skip("GPU present, skipping no-GPU test")
	}
}

func TestNVMLCollectorImplementsMetricsProvider(t *testing.T) {
	// Verify *NVMLCollector satisfies MetricsProvider at compile time
	var _ MetricsProvider = (*NVMLCollector)(nil)
}

func TestCollectorImplementsMetricsProvider(t *testing.T) {
	// Verify Collector satisfies MetricsProvider at compile time
	var _ MetricsProvider = Collector{}
}

func TestNewNVMLCollectorMultiGPU(t *testing.T) {
	collector, err := NewNVMLCollector()
	if err != nil {
		t.Skip("NVML not available, skipping multi-GPU test")
	}
	defer collector.Close()
	if len(collector.devices) == 0 {
		t.Fatal("expected at least 1 device")
	}
}

func TestNVMLCollectorCollectReturnsSlice(t *testing.T) {
	collector, err := NewNVMLCollector()
	if err != nil {
		t.Skip("NVML not available, skipping collect test")
	}
	defer collector.Close()
	metrics, err := collector.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric entry")
	}
	for i, m := range metrics {
		if m.GPUUUID == "" {
			t.Errorf("GPU %d: expected non-empty UUID", i)
		}
	}
}
