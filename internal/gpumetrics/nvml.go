package gpumetrics

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

type NVMLCollector struct {
	devices []nvml.Device
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

	devices := make([]nvml.Device, 0, count)
	for i := 0; i < count; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			nvml.Shutdown()
			return nil, fmt.Errorf("nvml device %d get handle failed: %s", i, nvml.ErrorString(ret))
		}
		devices = append(devices, device)
	}

	return &NVMLCollector{devices: devices}, nil
}

func (c *NVMLCollector) Collect() ([]GPUMetrics, error) {
	metrics := make([]GPUMetrics, 0, len(c.devices))
	for i, device := range c.devices {
		uuid, ret := device.GetUUID()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvml device %d get uuid failed: %s", i, nvml.ErrorString(ret))
		}

		memInfo, ret := device.GetMemoryInfo()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvml device %d get memory info failed: %s", i, nvml.ErrorString(ret))
		}

		util, ret := device.GetUtilizationRates()
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvml device %d get utilization failed: %s", i, nvml.ErrorString(ret))
		}

		temp, ret := device.GetTemperature(nvml.TEMPERATURE_GPU)
		if ret != nvml.SUCCESS {
			return nil, fmt.Errorf("nvml device %d get temperature failed: %s", i, nvml.ErrorString(ret))
		}

		metrics = append(metrics, GPUMetrics{
			GPUUUID:     uuid,
			MemoryUsed:  memInfo.Used,
			MemoryTotal: memInfo.Total,
			Utilization: util.Gpu,
			Temperature: uint32(temp),
		})
	}
	return metrics, nil
}

func (c *NVMLCollector) Close() {
	nvml.Shutdown()
}
