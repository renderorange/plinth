package gpumetrics

import (
	"fmt"
	"strings"
)

type GPUMetrics struct {
	GPUUUID     string
	MemoryUsed  uint64
	MemoryTotal uint64
	Utilization uint32
	Temperature uint32
}

type CollectFunc func() ([]GPUMetrics, error)

type Collector struct {
	collectFn CollectFunc
}

func NewCollector(fn CollectFunc) Collector {
	return Collector{collectFn: fn}
}

func (c Collector) Collect() ([]GPUMetrics, error) {
	return c.collectFn()
}

type MetricsProvider interface {
	Collect() ([]GPUMetrics, error)
}

func FormatPrometheus(metrics []GPUMetrics) string {
	var b strings.Builder

	b.WriteString("# HELP gpu_memory_used_bytes GPU memory used in bytes\n")
	b.WriteString("# TYPE gpu_memory_used_bytes gauge\n")
	b.WriteString("# HELP gpu_memory_total_bytes GPU total memory in bytes\n")
	b.WriteString("# TYPE gpu_memory_total_bytes gauge\n")
	b.WriteString("# HELP gpu_utilization_percent GPU utilization percentage\n")
	b.WriteString("# TYPE gpu_utilization_percent gauge\n")
	b.WriteString("# HELP gpu_temperature_celsius GPU temperature in celsius\n")
	b.WriteString("# TYPE gpu_temperature_celsius gauge\n")

	for _, m := range metrics {
		fmt.Fprintf(&b, "gpu_memory_used_bytes{gpu=%q} %d\n", m.GPUUUID, m.MemoryUsed)
		fmt.Fprintf(&b, "gpu_memory_total_bytes{gpu=%q} %d\n", m.GPUUUID, m.MemoryTotal)
		fmt.Fprintf(&b, "gpu_utilization_percent{gpu=%q} %d\n", m.GPUUUID, m.Utilization)
		fmt.Fprintf(&b, "gpu_temperature_celsius{gpu=%q} %d\n", m.GPUUUID, m.Temperature)
	}

	return b.String()
}
