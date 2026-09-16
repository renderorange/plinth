package gpumetrics

import "fmt"

type GPUMetrics struct {
	MemoryUsed  uint64
	MemoryTotal uint64
	Utilization uint32
	Temperature uint32
}

type CollectFunc func() (GPUMetrics, error)

type Collector struct {
	collectFn CollectFunc
}

func NewCollector(fn CollectFunc) Collector {
	return Collector{collectFn: fn}
}

func (c Collector) Collect() (GPUMetrics, error) {
	return c.collectFn()
}

type MetricsProvider interface {
	Collect() (GPUMetrics, error)
}

func FormatPrometheus(m GPUMetrics) string {
	return fmt.Sprintf(`# HELP gpu_memory_used_bytes GPU memory used in bytes
# TYPE gpu_memory_used_bytes gauge
gpu_memory_used_bytes %s
# HELP gpu_memory_total_bytes GPU total memory in bytes
# TYPE gpu_memory_total_bytes gauge
gpu_memory_total_bytes %s
# HELP gpu_utilization_percent GPU utilization percentage
# TYPE gpu_utilization_percent gauge
gpu_utilization_percent %d
# HELP gpu_temperature_celsius GPU temperature in celsius
# TYPE gpu_temperature_celsius gauge
gpu_temperature_celsius %d
`,
		fmt.Sprintf("%e", float64(m.MemoryUsed)),
		fmt.Sprintf("%e", float64(m.MemoryTotal)),
		m.Utilization,
		m.Temperature,
	)
}
