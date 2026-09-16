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
