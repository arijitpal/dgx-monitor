package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// cpuSample holds raw jiffies from /proc/stat for one CPU core.
type cpuSample struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
	steal   uint64
}

func (s cpuSample) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s cpuSample) active() uint64 {
	return s.user + s.nice + s.system + s.irq + s.softirq + s.steal
}

// CPUMetrics holds processed CPU metrics ready for display.
type CPUMetrics struct {
	NumCores   int
	CoreUsages []float64 // 0–100 per core
	AvgUsage   float64
	FreqsMHz   []float64
	MinFreqMHz float64
	MaxFreqMHz float64
	AvgFreqMHz float64
}

// SysMemMetrics holds system-wide memory (= unified memory on DGX Spark).
type SysMemMetrics struct {
	TotalBytes  uint64
	UsedBytes   uint64
	UsedPercent float64
}

var prevCPUSamples []cpuSample

func readCPUSamples() ([]cpuSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var samples []cpuSample
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Match per-core lines: cpu0, cpu1, … (skip the aggregate "cpu " line)
		if len(line) < 4 || !strings.HasPrefix(line, "cpu") || line[3] == ' ' {
			if len(samples) > 0 {
				break
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		var s cpuSample
		ptrs := []*uint64{&s.user, &s.nice, &s.system, &s.idle, &s.iowait, &s.irq, &s.softirq, &s.steal}
		for i, p := range ptrs {
			*p, _ = strconv.ParseUint(fields[i+1], 10, 64)
		}
		samples = append(samples, s)
	}
	return samples, scanner.Err()
}

func coreUsage(prev, curr cpuSample) float64 {
	totalDiff := float64(curr.total() - prev.total())
	if totalDiff <= 0 {
		return 0
	}
	activeDiff := float64(curr.active() - prev.active())
	if activeDiff < 0 {
		activeDiff = 0
	}
	return (activeDiff / totalDiff) * 100.0
}

func readFrequencies(n int) (freqs []float64, minMHz, maxMHz, avgMHz float64) {
	freqs = make([]float64, n)
	minMHz = 1e18
	var total float64
	var count int

	for i := 0; i < n; i++ {
		// Try scaling_cur_freq (governor-controlled), fall back to cpuinfo_cur_freq (hardware)
		for _, name := range []string{"scaling_cur_freq", "cpuinfo_cur_freq"} {
			path := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/%s", i, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
			if err != nil {
				continue
			}
			mhz := val / 1000.0 // kHz → MHz
			freqs[i] = mhz
			total += mhz
			count++
			if mhz < minMHz {
				minMHz = mhz
			}
			if mhz > maxMHz {
				maxMHz = mhz
			}
			break
		}
	}
	if count == 0 {
		minMHz = 0
		return
	}
	avgMHz = total / float64(count)
	return
}

// CollectCPU gathers per-core usage (delta since last call) and frequencies.
func CollectCPU() (*CPUMetrics, error) {
	curr, err := readCPUSamples()
	if err != nil {
		return nil, err
	}

	m := &CPUMetrics{
		NumCores:   len(curr),
		CoreUsages: make([]float64, len(curr)),
	}

	if len(prevCPUSamples) == len(curr) {
		var sum float64
		for i := range curr {
			u := coreUsage(prevCPUSamples[i], curr[i])
			m.CoreUsages[i] = u
			sum += u
		}
		if len(curr) > 0 {
			m.AvgUsage = sum / float64(len(curr))
		}
	}
	prevCPUSamples = curr

	m.FreqsMHz, m.MinFreqMHz, m.MaxFreqMHz, m.AvgFreqMHz = readFrequencies(len(curr))
	return m, nil
}

// CollectSysMem reads /proc/meminfo for unified-memory totals.
func CollectSysMem() (*SysMemMetrics, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var memTotal, memAvailable uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			memTotal = val * 1024
		case "MemAvailable:":
			memAvailable = val * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	used := memTotal - memAvailable
	pct := 0.0
	if memTotal > 0 {
		pct = float64(used) / float64(memTotal) * 100.0
	}
	return &SysMemMetrics{
		TotalBytes:  memTotal,
		UsedBytes:   used,
		UsedPercent: pct,
	}, nil
}
