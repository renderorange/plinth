package gpumetrics

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type NVMLCollector struct {
	device nvml.Device
}

func NewNVMLCollector() (*NVMLCollector, error) {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("nvml init failed: %s", nvml.ErrorString(ret))
	}

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		nvml.Shutdown()
		return nil, fmt.Errorf("nvml device get count failed: %s", nvml.ErrorString(ret))
	}
	if count == 0 {
		nvml.Shutdown()
		return nil, fmt.Errorf("no GPU devices found")
	}

	// NOTE: only the first GPU (device index 0) is collected. Multi-GPU nodes
	// will under-report memory/utilization/temperature for the remaining GPUs.
	device, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		nvml.Shutdown()
		return nil, fmt.Errorf("nvml device get handle failed: %s", nvml.ErrorString(ret))
	}

	return &NVMLCollector{device: device}, nil
}

func (c *NVMLCollector) Collect() (GPUMetrics, error) {
	memInfo, ret := c.device.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return GPUMetrics{}, fmt.Errorf("nvml get memory info failed: %s", nvml.ErrorString(ret))
	}

	util, ret := c.device.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return GPUMetrics{}, fmt.Errorf("nvml get utilization failed: %s", nvml.ErrorString(ret))
	}

	temp, ret := c.device.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		return GPUMetrics{}, fmt.Errorf("nvml get temperature failed: %s", nvml.ErrorString(ret))
	}

	return GPUMetrics{
		MemoryUsed:  memInfo.Used,
		MemoryTotal: memInfo.Total,
		Utilization: util.Gpu,
		Temperature: uint32(temp),
	}, nil
}

func (c *NVMLCollector) Close() {
	nvml.Shutdown()
}
