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
	coresPerCol = 7           // cores per column in the % display
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

// titleStatusStyle returns a bold title style colored by status percentage.
// Used to color the leading status dot (●) in gauge titles.
func titleStatusStyle(pct float64) ui.Style {
	return ui.NewStyle(gaugeColor(pct), ui.ColorClear, ui.ModifierBold)
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

	StartPublicIPRefresher()

	var gpuHistory, memHistory []float64
	var rxHistory, txHistory []float64
	var currentGPU, peakGPU float64
	var currentMemGiB, peakMemGiB, totalMemGiB float64
	var currentRxBps, currentTxBps, peakRxBps, peakTxBps float64

	// ── Widgets ────────────────────────────────────────────────────────────────

	// Shared "hacker terminal" styles: green-on-black, bold titles.
	greenBorder := ui.NewStyle(ui.ColorGreen)
	greenBoldTitle := ui.NewStyle(ui.ColorGreen, ui.ColorClear, ui.ModifierBold)
	greenText := ui.NewStyle(ui.ColorGreen)
	greenBoldLabel := ui.NewStyle(ui.ColorGreen, ui.ColorClear, ui.ModifierBold)
	axes := ui.ColorGreen

	header := widgets.NewParagraph()
	header.Title = " DGX SPARK MONITOR "
	header.TitleStyle = greenBoldTitle
	header.BorderStyle = greenBorder
	header.TextStyle = greenText
	header.PaddingTop = 0

	// CPU section
	cpuCores := widgets.NewParagraph()
	cpuCores.Title = " cpu.cores "
	cpuCores.TitleStyle = greenBoldTitle
	cpuCores.BorderStyle = greenBorder
	cpuCores.TextStyle = greenText
	cpuCores.PaddingLeft = 1

	cpuGauge := widgets.NewGauge()
	cpuGauge.Title = " cpu.avg "
	cpuGauge.TitleStyle = greenBoldTitle
	cpuGauge.BarColor = ui.ColorGreen
	cpuGauge.BorderStyle = greenBorder
	cpuGauge.LabelStyle = greenBoldLabel

	freqPara := widgets.NewParagraph()
	freqPara.Title = " cpu.freq "
	freqPara.TitleStyle = greenBoldTitle
	freqPara.BorderStyle = greenBorder
	freqPara.TextStyle = greenText
	freqPara.PaddingLeft = 1

	// GPU section
	gpuUtilGauge := widgets.NewGauge()
	gpuUtilGauge.Title = " ● gpu.compute "
	gpuUtilGauge.TitleStyle = greenBoldTitle
	gpuUtilGauge.BarColor = ui.ColorGreen
	gpuUtilGauge.BorderStyle = greenBorder
	gpuUtilGauge.LabelStyle = greenBoldLabel

	memGauge := widgets.NewGauge()
	memGauge.Title = " ● mem.unified "
	memGauge.TitleStyle = greenBoldTitle
	memGauge.BarColor = ui.ColorGreen
	memGauge.BorderStyle = greenBorder
	memGauge.LabelStyle = greenBoldLabel

	powerGauge := widgets.NewGauge()
	powerGauge.Title = " ● gpu.power "
	powerGauge.TitleStyle = greenBoldTitle
	powerGauge.BarColor = ui.ColorGreen
	powerGauge.BorderStyle = greenBorder
	powerGauge.LabelStyle = greenBoldLabel

	gpuInfoPara := widgets.NewParagraph()
	gpuInfoPara.Title = " gpu.info "
	gpuInfoPara.TitleStyle = greenBoldTitle
	gpuInfoPara.BorderStyle = greenBorder
	gpuInfoPara.TextStyle = greenText
	gpuInfoPara.PaddingLeft = 1

	// Plot: GPU compute history (scatter dots, scrolls right-to-left)
	gpuPlot := widgets.NewPlot()
	gpuPlot.Title = " gpu.compute.history "
	gpuPlot.TitleStyle = greenBoldTitle
	gpuPlot.PlotType = widgets.LineChart
	gpuPlot.HorizontalScale = 2
	gpuPlot.LineColors = []ui.Color{ui.ColorGreen}
	gpuPlot.AxesColor = axes
	gpuPlot.MaxVal = 100
	gpuPlot.Data = [][]float64{{0, 0}}
	gpuPlot.BorderStyle = greenBorder

	// Plot: unified memory history in GiB (scatter dots, scrolls right-to-left)
	memPlot := widgets.NewPlot()
	memPlot.Title = " mem.unified.history "
	memPlot.TitleStyle = greenBoldTitle
	memPlot.PlotType = widgets.LineChart
	memPlot.HorizontalScale = 2
	memPlot.LineColors = []ui.Color{ui.ColorGreen}
	memPlot.AxesColor = axes
	memPlot.Data = [][]float64{{0, 0}}
	memPlot.BorderStyle = greenBorder

	// Network section: interface list + bandwidth plot
	netInfo := widgets.NewParagraph()
	netInfo.Title = " net.interfaces "
	netInfo.TitleStyle = greenBoldTitle
	netInfo.BorderStyle = greenBorder
	netInfo.TextStyle = greenText
	netInfo.PaddingLeft = 1

	netPlot := widgets.NewPlot()
	netPlot.Title = " net.bandwidth "
	netPlot.TitleStyle = greenBoldTitle
	netPlot.PlotType = widgets.LineChart
	netPlot.HorizontalScale = 2
	netPlot.LineColors = []ui.Color{ui.ColorGreen, ui.ColorRed}
	netPlot.AxesColor = axes
	netPlot.ShowAxes = false
	netPlot.Data = [][]float64{{0, 0}, {0, 0}}
	netPlot.BorderStyle = greenBorder

	// ── Grid ──────────────────────────────────────────────────────────────────

	grid := ui.NewGrid()
	setGrid := func() {
		w, h := ui.TerminalDimensions()
		grid.SetRect(0, 0, w, h)
		grid.Set(
			ui.NewRow(0.07, header),
			ui.NewRow(0.13,
				ui.NewCol(0.50, cpuCores),
				ui.NewCol(0.25, cpuGauge),
				ui.NewCol(0.25, freqPara),
			),
			ui.NewRow(0.11,
				ui.NewCol(0.28, gpuUtilGauge),
				ui.NewCol(0.28, memGauge),
				ui.NewCol(0.22, powerGauge),
				ui.NewCol(0.22, gpuInfoPara),
			),
			ui.NewRow(0.38,
				ui.NewCol(0.50, gpuPlot),
				ui.NewCol(0.50, memPlot),
			),
			ui.NewRow(0.31,
				ui.NewCol(0.50, netInfo),
				ui.NewCol(0.50, netPlot),
			),
		)
	}
	setGrid()

	// ── Update function ────────────────────────────────────────────────────────

	update := func() {
		hostname, _ := os.Hostname()
		local := LocalIP()
		if local == "" {
			local = "—"
		}
		public := PublicIP()
		if public == "" {
			public = "—"
		}
		header.Text = fmt.Sprintf(
			" [$](fg:green,modifier:bold) "+
				"[host](fg:green)=[%s](fg:green,modifier:bold)  "+
				"[time](fg:green)=[%s](fg:green,modifier:bold)  "+
				"[local](fg:green)=[%s](fg:green,modifier:bold)  "+
				"[public](fg:green)=[%s](fg:green,modifier:bold)   "+
				"[<q>](fg:red,modifier:bold) [exit](fg:red)",
			hostname,
			time.Now().Format("2006-01-02 15:04:05"),
			local, public,
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
					" avg   [%.0f MHz](fg:green,modifier:bold)\n"+
						" max   [%.0f MHz](%s)\n"+
						" min   [%.0f MHz](fg:green)\n"+
						" cores [%d](fg:green,modifier:bold)",
					cpu.AvgFreqMHz,
					cpu.MaxFreqMHz, colorForPct(0),
					cpu.MinFreqMHz,
					cpu.NumCores,
				)
			} else {
				freqPara.Text = fmt.Sprintf(" cores [%d](fg:green,modifier:bold)\n freq  [N/A](fg:red)", cpu.NumCores)
			}
		}

		// ── Unified memory (from /proc/meminfo) ───────────────────────────────
		mem, _ := CollectSysMem()
		if mem != nil {
			memPct := clamp(int(math.Round(mem.UsedPercent)))
			memGauge.Percent = memPct
			memGauge.BarColor = gaugeColor(mem.UsedPercent)
			memGauge.TitleStyle = titleStatusStyle(mem.UsedPercent)
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
			gpuUtilGauge.TitleStyle = titleStatusStyle(float64(d.ComputeUtil))
			gpuUtilGauge.Label = fmt.Sprintf("%d%%   @ %d MHz", utilPct, d.GPUClockMHz)

			pwPctF := d.PowerW / maxPowerW * 100.0
			pwPct := clamp(int(math.Round(pwPctF)))
			powerGauge.Percent = pwPct
			powerGauge.BarColor = gaugeColor(pwPctF)
			powerGauge.TitleStyle = titleStatusStyle(pwPctF)
			powerGauge.Label = fmt.Sprintf("%.0fW / %.0fW", d.PowerW, maxPowerW)

			gpuInfoPara.Text = fmt.Sprintf(
				" [%s](fg:green,modifier:bold)\n"+
					" temp  [%d°C](%s)\n"+
					" gmem  [%d MHz](fg:green)\n"+
					" vram  [%.1f GiB](fg:green,modifier:bold)",
				d.Name,
				d.TempC, colorForPct(float64(d.TempC)/1.2),
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
			gpuUtilGauge.TitleStyle = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)
			powerGauge.Percent = 0
			powerGauge.Label = "N/A"
			powerGauge.TitleStyle = ui.NewStyle(ui.ColorWhite, ui.ColorClear, ui.ModifierBold)
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

		// ── Network ───────────────────────────────────────────────────────────
		nm, _ := CollectNet()
		if nm != nil {
			var sb strings.Builder
			// Color legend matching the net.bandwidth plot lines.
			sb.WriteString(" graph legend → ")
			sb.WriteString("[━ RX (incoming)](fg:green,modifier:bold)")
			sb.WriteString("   ")
			sb.WriteString("[━ TX (outgoing)](fg:red,modifier:bold)")
			sb.WriteString("\n")
			for _, iface := range nm.Interfaces {
				var dot, status string
				switch {
				case iface.IsUp && iface.HasCarrier:
					dot = "[●](fg:green)"
					status = "[ ACTIVE ](fg:green,modifier:bold)"
				case iface.IsUp:
					dot = "[●](fg:yellow)"
					status = "[ NO LINK](fg:yellow)"
				default:
					dot = "[○](fg:red)"
					status = "[INACTIVE](fg:red)"
				}
				kindColor := "fg:white"
				switch iface.Kind {
				case IfaceWireless:
					kindColor = "fg:green,modifier:bold"
				case IfaceEthernet:
					kindColor = "fg:green,modifier:bold"
				case IfaceVirtual:
					kindColor = "fg:white"
				case IfaceLoopback:
					kindColor = "fg:white"
				}
				sb.WriteString(fmt.Sprintf(
					" %s %-12s [%s](%s) %s  ↓ [%s](fg:green)  ↑ [%s](fg:red)\n",
					dot, iface.Name, iface.Kind.Label(), kindColor, status,
					formatBps(iface.RxBps), formatBps(iface.TxBps),
				))
			}
			netInfo.Text = sb.String()

			currentRxBps = nm.TotalRxBps
			currentTxBps = nm.TotalTxBps
			if currentRxBps > peakRxBps {
				peakRxBps = currentRxBps
			}
			if currentTxBps > peakTxBps {
				peakTxBps = currentTxBps
			}
			rxHistory = appendHistory(rxHistory, currentRxBps)
			txHistory = appendHistory(txHistory, currentTxBps)
		} else {
			rxHistory = appendHistory(rxHistory, 0)
			txHistory = appendHistory(txHistory, 0)
		}

		if len(rxHistory) >= 2 && len(txHistory) >= 2 {
			netPlot.Data = [][]float64{rxHistory, txHistory}
		}
		netPlot.Title = fmt.Sprintf(
			" Network  ↓ %s (peak %s)  ↑ %s (peak %s) ",
			formatBps(currentRxBps), formatBps(peakRxBps),
			formatBps(currentTxBps), formatBps(peakTxBps),
		)

		ui.Render(grid)
	}

	// Prime CPU and network deltas (first calls establish baselines).
	CollectCPU()
	CollectNet()
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
