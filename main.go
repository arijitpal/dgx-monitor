package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

const (
	historyLen  = 120         // 120 seconds of rolling history
	refreshRate = time.Second // 1 Hz refresh
	coresPerCol = 8           // cores per column in the % display
	maxPowerW   = 140.0       // max GPU power scale (watts)
)

// colorForPct returns a termui markup color tag based on load level.
func colorForPct(pct float64) string {
	switch {
	case pct > 80:
		return "fg:red"
	case pct > 50:
		return "fg:yellow"
	default:
		return "fg:green"
	}
}

// clamp ensures an int percentage stays within [0, 100].
func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// gaugeColor picks a ui.Color matching load level.
func gaugeColor(pct float64) ui.Color {
	switch {
	case pct > 80:
		return ui.ColorRed
	case pct > 50:
		return ui.ColorYellow
	default:
		return ui.ColorGreen
	}
}

// appendHistory appends a value to a fixed-length rolling slice.
func appendHistory(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > historyLen {
		h = h[len(h)-historyLen:]
	}
	return h
}

func main() {
	if err := ui.Init(); err != nil {
		log.Fatalf("termui init: %v", err)
	}
	defer ui.Close()

	gpu := NewGPUCollector()
	defer gpu.Close()

	var gpuHistory, memHistory []float64
	var currentGPU, peakGPU float64
	var currentMemGiB, peakMemGiB, totalMemGiB float64

	// ── Widgets ────────────────────────────────────────────────────────────────

	header := widgets.NewParagraph()
	header.Title = "  DGX Spark Monitor  "
	header.TitleStyle = ui.NewStyle(ui.ColorGreen, ui.ColorClear, ui.ModifierBold)
	header.BorderStyle = ui.NewStyle(ui.ColorGreen)
	header.PaddingTop = 0

	// CPU section
	cpuCores := widgets.NewParagraph()
	cpuCores.Title = " CPU Cores % "
	cpuCores.BorderStyle = ui.NewStyle(ui.ColorCyan)
	cpuCores.PaddingLeft = 1

	cpuGauge := widgets.NewGauge()
	cpuGauge.Title = " CPU Avg Usage "
	cpuGauge.BarColor = ui.ColorCyan
	cpuGauge.BorderStyle = ui.NewStyle(ui.ColorCyan)
	cpuGauge.LabelStyle = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)

	freqPara := widgets.NewParagraph()
	freqPara.Title = " CPU Frequency "
	freqPara.BorderStyle = ui.NewStyle(ui.ColorCyan)
	freqPara.PaddingLeft = 1

	// GPU section
	gpuUtilGauge := widgets.NewGauge()
	gpuUtilGauge.Title = " GPU Compute "
	gpuUtilGauge.BarColor = ui.ColorGreen
	gpuUtilGauge.BorderStyle = ui.NewStyle(ui.ColorGreen)
	gpuUtilGauge.LabelStyle = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)

	memGauge := widgets.NewGauge()
	memGauge.Title = " Unified Memory "
	memGauge.BarColor = ui.ColorYellow
	memGauge.BorderStyle = ui.NewStyle(ui.ColorYellow)
	memGauge.LabelStyle = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)

	powerGauge := widgets.NewGauge()
	powerGauge.Title = " GPU Power "
	powerGauge.BarColor = ui.ColorMagenta
	powerGauge.BorderStyle = ui.NewStyle(ui.ColorMagenta)
	powerGauge.LabelStyle = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)

	gpuInfoPara := widgets.NewParagraph()
	gpuInfoPara.Title = " GPU Info "
	gpuInfoPara.BorderStyle = ui.NewStyle(ui.ColorGreen)
	gpuInfoPara.PaddingLeft = 1

	// Plot: GPU compute history (2D line, scrolls right-to-left)
	gpuPlot := widgets.NewPlot()
	gpuPlot.Title = " GPU Compute %  (2 min history — press q to quit) "
	gpuPlot.PlotType = widgets.LineChart
	gpuPlot.LineColors = []ui.Color{ui.ColorGreen}
	gpuPlot.AxesColor = ui.ColorWhite
	gpuPlot.MaxVal = 100
	gpuPlot.Data = [][]float64{{0, 0}}
	gpuPlot.BorderStyle = ui.NewStyle(ui.ColorGreen)

	// Plot: unified memory history in GiB (2D line, scrolls right-to-left)
	memPlot := widgets.NewPlot()
	memPlot.Title = " Unified Memory GiB  (2 min history) "
	memPlot.PlotType = widgets.LineChart
	memPlot.LineColors = []ui.Color{ui.ColorYellow}
	memPlot.AxesColor = ui.ColorWhite
	memPlot.Data = [][]float64{{0, 0}}
	memPlot.BorderStyle = ui.NewStyle(ui.ColorYellow)

	// ── Grid ──────────────────────────────────────────────────────────────────

	grid := ui.NewGrid()
	setGrid := func() {
		w, h := ui.TerminalDimensions()
		grid.SetRect(0, 0, w, h)
		grid.Set(
			ui.NewRow(0.07, header),
			ui.NewRow(0.26,
				ui.NewCol(0.50, cpuCores),
				ui.NewCol(0.25, cpuGauge),
				ui.NewCol(0.25, freqPara),
			),
			ui.NewRow(0.22,
				ui.NewCol(0.28, gpuUtilGauge),
				ui.NewCol(0.28, memGauge),
				ui.NewCol(0.22, powerGauge),
				ui.NewCol(0.22, gpuInfoPara),
			),
			ui.NewRow(0.225, gpuPlot),
			ui.NewRow(0.225, memPlot),
		)
	}
	setGrid()

	// ── Update function ────────────────────────────────────────────────────────

	update := func() {
		hostname, _ := os.Hostname()
		header.Text = fmt.Sprintf(
			"  Host: [%s](fg:white,modifier:bold)   Time: [%s](fg:white)   "+
				"Press [q](fg:red,modifier:bold) or [Ctrl-C](fg:red,modifier:bold) to quit",
			hostname,
			time.Now().Format("2006-01-02  15:04:05"),
		)

		// ── CPU ───────────────────────────────────────────────────────────────
		cpu, err := CollectCPU()
		if err == nil && cpu != nil {
			// Per-core % usage, laid out column-wise
			var sb strings.Builder
			n := len(cpu.CoreUsages)
			rows := coresPerCol
			cols := (n + rows - 1) / rows
			idxWidth := 2
			if n > 99 {
				idxWidth = 3
			}
			cellFmt := fmt.Sprintf("[C%%0%dd:%%3.0f%%%%](%%s)  ", idxWidth)
			for r := 0; r < rows; r++ {
				for c := 0; c < cols; c++ {
					idx := c*rows + r
					if idx >= n {
						continue
					}
					u := cpu.CoreUsages[idx]
					sb.WriteString(fmt.Sprintf(cellFmt, idx, u, colorForPct(u)))
				}
				if r < rows-1 {
					sb.WriteByte('\n')
				}
			}
			cpuCores.Text = sb.String()

			pct := clamp(int(math.Round(cpu.AvgUsage)))
			cpuGauge.Percent = pct
			cpuGauge.Label = fmt.Sprintf("%d%%", pct)
			cpuGauge.BarColor = gaugeColor(cpu.AvgUsage)

			if cpu.AvgFreqMHz > 0 {
				freqPara.Text = fmt.Sprintf(
					" Avg  [%.0f MHz](fg:white,modifier:bold)\n"+
						" Max  [%.0f MHz](%s)\n"+
						" Min  [%.0f MHz](fg:cyan)\n"+
						" Cores [%d](fg:white)",
					cpu.AvgFreqMHz,
					cpu.MaxFreqMHz, colorForPct(0),
					cpu.MinFreqMHz,
					cpu.NumCores,
				)
			} else {
				freqPara.Text = fmt.Sprintf(" Cores [%d](fg:white)\n Freq  [N/A](fg:red)", cpu.NumCores)
			}
		}

		// ── Unified memory (from /proc/meminfo) ───────────────────────────────
		mem, _ := CollectSysMem()
		if mem != nil {
			memPct := clamp(int(math.Round(mem.UsedPercent)))
			memGauge.Percent = memPct
			memGauge.BarColor = gaugeColor(mem.UsedPercent)
			memGauge.Label = fmt.Sprintf(
				"%d%%   %.1f / %.1f GiB",
				memPct,
				float64(mem.UsedBytes)/1073741824,
				float64(mem.TotalBytes)/1073741824,
			)
			currentMemGiB = float64(mem.UsedBytes) / 1073741824
			totalMemGiB = float64(mem.TotalBytes) / 1073741824
			if currentMemGiB > peakMemGiB {
				peakMemGiB = currentMemGiB
			}
			memHistory = appendHistory(memHistory, currentMemGiB)
		} else {
			memHistory = appendHistory(memHistory, 0)
		}

		// ── GPU ───────────────────────────────────────────────────────────────
		devs, _ := gpu.Collect()
		if len(devs) > 0 {
			d := devs[0]

			utilPct := clamp(int(d.ComputeUtil))
			gpuUtilGauge.Percent = utilPct
			gpuUtilGauge.BarColor = gaugeColor(float64(d.ComputeUtil))
			gpuUtilGauge.Label = fmt.Sprintf("%d%%   @ %d MHz", utilPct, d.GPUClockMHz)

			pwPctF := d.PowerW / maxPowerW * 100.0
			pwPct := clamp(int(math.Round(pwPctF)))
			powerGauge.Percent = pwPct
			powerGauge.BarColor = gaugeColor(pwPctF)
			powerGauge.Label = fmt.Sprintf("%.0fW / %.0fW", d.PowerW, maxPowerW)

			gpuInfoPara.Text = fmt.Sprintf(
				" [%s](fg:green,modifier:bold)\n"+
					" Temp  [%d°C](%s)\n"+
					" GMem  [%d MHz](fg:cyan)\n"+
					" VRAMu [%.1f GiB](fg:white)",
				d.Name,
				d.TempC, colorForPct(float64(d.TempC)/1.2), // rough heat scale
				d.MemClockMHz,
				float64(d.MemUsed)/1073741824,
			)

			currentGPU = float64(d.ComputeUtil)
			if currentGPU > peakGPU {
				peakGPU = currentGPU
			}
			gpuHistory = appendHistory(gpuHistory, currentGPU)
		} else {
			gpuUtilGauge.Percent = 0
			gpuUtilGauge.Label = "N/A — no NVML device"
			powerGauge.Percent = 0
			powerGauge.Label = "N/A"
			gpuInfoPara.Text = " [No GPU / NVML unavailable](fg:red)"
			gpuHistory = appendHistory(gpuHistory, 0)
		}

		if len(gpuHistory) >= 2 {
			gpuPlot.Data = [][]float64{gpuHistory}
		}
		gpuPlot.Title = fmt.Sprintf(
			" GPU Compute %%  cur: %.0f%%  peak: %.0f%%  range: 0–100%% ",
			currentGPU, peakGPU,
		)

		if len(memHistory) >= 2 {
			memPlot.Data = [][]float64{memHistory}
		}
		if totalMemGiB > 0 {
			memPlot.MaxVal = totalMemGiB
		}
		memPlot.Title = fmt.Sprintf(
			" Unified Memory GiB  cur: %.1f  peak: %.1f  total: %.1f ",
			currentMemGiB, peakMemGiB, totalMemGiB,
		)

		ui.Render(grid)
	}

	// Prime CPU delta (first call establishes baseline; usage shown from second call)
	CollectCPU()
	time.Sleep(200 * time.Millisecond)
	update()

	ticker := time.NewTicker(refreshRate)
	defer ticker.Stop()

	for {
		select {
		case e := <-ui.PollEvents():
			switch e.ID {
			case "q", "<C-c>":
				return
			case "<Resize>":
				setGrid()
				ui.Clear()
				ui.Render(grid)
			}
		case <-ticker.C:
			update()
		}
	}
}
