package main

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// GPUDevice holds metrics for a single GPU at one point in time.
type GPUDevice struct {
	Index       int
	Name        string
	ComputeUtil uint32  // 0–100 %
	MemUtil     uint32  // 0–100 %
	MemUsed     uint64  // bytes
	MemTotal    uint64  // bytes
	MemPct      float64 // 0–100
	PowerW      float64 // watts
	PowerLimitW float64 // watts
	PowerPct    float64 // 0–100
	GPUClockMHz uint32
	MemClockMHz uint32
	TempC       uint32
}

// GPUCollector wraps the NVML lifecycle.
type GPUCollector struct {
	ok bool
}

// NewGPUCollector initialises NVML. Always returns a valid collector;
// check Available() to know whether GPU data will be populated.
func NewGPUCollector() *GPUCollector {
	ret := nvml.Init()
	return &GPUCollector{ok: ret == nvml.SUCCESS}
}

func (c *GPUCollector) Close() {
	if c.ok {
		nvml.Shutdown()
	}
}

func (c *GPUCollector) Available() bool { return c.ok }

// Collect returns current metrics for every detected GPU device.
func (c *GPUCollector) Collect() ([]GPUDevice, error) {
	if !c.ok {
		return nil, nil
	}

	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("DeviceGetCount: %s", nvml.ErrorString(ret))
	}

	devs := make([]GPUDevice, 0, count)
	for i := 0; i < count; i++ {
		h, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			continue
		}

		d := GPUDevice{Index: i}

		if name, r := nvml.DeviceGetName(h); r == nvml.SUCCESS {
			d.Name = name
		}
		if util, r := nvml.DeviceGetUtilizationRates(h); r == nvml.SUCCESS {
			d.ComputeUtil = util.Gpu
			d.MemUtil = util.Memory
		}
		if mem, r := nvml.DeviceGetMemoryInfo(h); r == nvml.SUCCESS {
			d.MemUsed = mem.Used
			d.MemTotal = mem.Total
			if mem.Total > 0 {
				d.MemPct = float64(mem.Used) / float64(mem.Total) * 100.0
			}
		}
		if pw, r := nvml.DeviceGetPowerUsage(h); r == nvml.SUCCESS {
			d.PowerW = float64(pw) / 1000.0
		}
		if pl, r := nvml.DeviceGetPowerManagementLimit(h); r == nvml.SUCCESS {
			d.PowerLimitW = float64(pl) / 1000.0
			if d.PowerLimitW > 0 {
				d.PowerPct = d.PowerW / d.PowerLimitW * 100.0
			}
		}
		if clk, r := nvml.DeviceGetClockInfo(h, nvml.CLOCK_GRAPHICS); r == nvml.SUCCESS {
			d.GPUClockMHz = clk
		}
		if clk, r := nvml.DeviceGetClockInfo(h, nvml.CLOCK_MEM); r == nvml.SUCCESS {
			d.MemClockMHz = clk
		}
		if temp, r := nvml.DeviceGetTemperature(h, nvml.TEMPERATURE_GPU); r == nvml.SUCCESS {
			d.TempC = temp
		}

		devs = append(devs, d)
	}
	return devs, nil
}
