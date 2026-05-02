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
	cpuColumns     = 3        // CPU cores rendered as 3 evenly-sized columns (column-wise filling)
	cpuBarMin      = 4        // min length of a per-core bar
	cpuBarMax      = 14       // max length of a per-core bar
	cpuCellPercent = 5        // chars reserved for " 100%" suffix in a cell
	maxPowerW   = 140.0       // max GPU power scale (watts)
	storageRefreshRate = 2 * time.Minute // /sys/block + statfs is comparatively expensive
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

// loadBar returns a width-character horizontal bar for a 0–100 percentage.
// The empty portion is rendered as spaces so the bar matches the gauge
// look of the GPU power widget (filled block on the left, plain
// background on the right — no ░ texture).
func loadBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(math.Round(pct / 100.0 * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat(" ", width-filled)
}

// appendHistory appends a value to a fixed-length rolling slice.
func appendHistory(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > historyLen {
		h = h[len(h)-historyLen:]
	}
	return h
}

// visibleBarCount reports how many bars of width barW with barGap spacing
// fit in a panel whose inner width is innerW.
func visibleBarCount(innerW, barW, barGap int) int {
	if innerW <= 0 {
		return 1
	}
	if barW < 1 {
		barW = 1
	}
	cell := barW + barGap
	if cell < 1 {
		cell = 1
	}
	n := innerW / cell
	if n < 1 {
		n = 1
	}
	return n
}

// tailBars returns the last n samples from history (or the whole slice if
// shorter) — keeps the rightmost bar always showing the newest sample.
func tailBars(history []float64, n int) []float64 {
	if len(history) > n {
		return history[len(history)-n:]
	}
	return history
}

func main() {
	if err := ui.Init(); err != nil {
		log.Fatalf("termui init: %v", err)
	}
	defer ui.Close()

	gpu := NewGPUCollector()
	defer gpu.Close()

	StartPublicIPRefresher()

	// Wi-Fi modal: only the hint shows when at least one wireless device exists.
	wifi := newWifiUI()
	wifiAvailable := len(WifiInterfaces()) > 0

	var gpuHistory, memHistory []float64
	var rxHistory, txHistory []float64
	var currentGPU, peakGPU float64
	var currentMemGiB, peakMemGiB, totalMemGiB float64
	var currentRxBps, currentTxBps, peakRxBps, peakTxBps float64
	var storageDevs []StorageDevice
	var storageLast time.Time

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
	cpuCores.WrapText = false // each cell row must stay on one visual line

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

	// suppress numeric labels above bars; with narrow bars they get
	// truncated to 2 chars and become useless. Live values still appear
	// in each chart's title.
	noNum := func(_ float64) string { return "" }

	// 256-color "deep green" — gentler on the eye while keeping the
	// hacker-terminal aesthetic. Falls back to standard green on
	// terminals that don't support 256 colors.
	darkGreen := ui.Color(22)

	// BarChart: GPU compute history (vertical bars, scrolls right-to-left)
	gpuBar := widgets.NewBarChart()
	gpuBar.Title = " gpu.compute.history "
	gpuBar.TitleStyle = greenBoldTitle
	gpuBar.BorderStyle = greenBorder
	gpuBar.BarColors = []ui.Color{darkGreen}
	gpuBar.LabelStyles = []ui.Style{ui.NewStyle(darkGreen)}
	gpuBar.NumStyles = []ui.Style{ui.NewStyle(darkGreen, ui.ColorClear, ui.ModifierBold)}
	gpuBar.NumFormatter = noNum
	gpuBar.BarWidth = 2
	gpuBar.BarGap = 0
	gpuBar.MaxVal = 100
	gpuBar.Data = []float64{0}
	gpuBar.Labels = []string{""}

	// BarChart: unified memory history in GiB
	memBar := widgets.NewBarChart()
	memBar.Title = " mem.unified.history "
	memBar.TitleStyle = greenBoldTitle
	memBar.BorderStyle = greenBorder
	memBar.BarColors = []ui.Color{darkGreen}
	memBar.LabelStyles = []ui.Style{ui.NewStyle(darkGreen)}
	memBar.NumStyles = []ui.Style{ui.NewStyle(darkGreen, ui.ColorClear, ui.ModifierBold)}
	memBar.NumFormatter = noNum
	memBar.BarWidth = 2
	memBar.BarGap = 0
	memBar.Data = []float64{0}
	memBar.Labels = []string{""}

	// Network section: interface list + connections list + stacked bandwidth bar chart
	netInfo := widgets.NewParagraph()
	netInfo.Title = " net.interfaces "
	netInfo.TitleStyle = greenBoldTitle
	netInfo.BorderStyle = greenBorder
	netInfo.TextStyle = greenText
	netInfo.PaddingLeft = 1

	netConns := widgets.NewParagraph()
	netConns.Title = " net.connections "
	netConns.TitleStyle = greenBoldTitle
	netConns.BorderStyle = greenBorder
	netConns.TextStyle = greenText
	netConns.PaddingLeft = 1

	// Plot: per-second RX (green) and TX (red) line chart, scrolls
	// right-to-left as new samples arrive. Replaces the previous
	// stacked bar chart for a cleaner look.
	netPlot := widgets.NewPlot()
	netPlot.Title = " net.bandwidth "
	netPlot.TitleStyle = greenBoldTitle
	netPlot.BorderStyle = greenBorder
	netPlot.AxesColor = ui.ColorGreen
	netPlot.LineColors = []ui.Color{ui.ColorGreen, ui.ColorRed}
	netPlot.Marker = widgets.MarkerBraille
	netPlot.PlotType = widgets.LineChart
	netPlot.HorizontalScale = 1
	netPlot.ShowAxes = true
	netPlot.Data = [][]float64{{0, 0}, {0, 0}}

	// Storage section: enumerate physical block devices, show usage
	// gauges for those we can reach a mount point for, and a status
	// line for everything else.
	storagePara := widgets.NewParagraph()
	storagePara.Title = " storage "
	storagePara.TitleStyle = greenBoldTitle
	storagePara.BorderStyle = greenBorder
	storagePara.TextStyle = greenText
	storagePara.PaddingLeft = 1
	storagePara.WrapText = false

	_ = axes // kept for future plot-style additions

	// ── Grid ──────────────────────────────────────────────────────────────────

	grid := ui.NewGrid()
	setGrid := func() {
		w, h := ui.TerminalDimensions()
		grid.SetRect(0, 0, w, h)
		// cpu.avg + cpu.freq used to take the right 50% of this row at
		// 25% each; the user asked for 2/3 of that combined width to be
		// reclaimed for the storage panel, leaving the gauges stacked
		// vertically in the remaining 1/3.
		grid.Set(
			ui.NewRow(0.06, header),
			ui.NewRow(0.30,
				ui.NewCol(0.50, cpuCores),
				ui.NewCol(0.166,
					ui.NewRow(0.5, cpuGauge),
					ui.NewRow(0.5, freqPara),
				),
				ui.NewCol(0.334, storagePara),
			),
			ui.NewRow(0.09,
				ui.NewCol(0.28, gpuUtilGauge),
				ui.NewCol(0.28, memGauge),
				ui.NewCol(0.22, powerGauge),
				ui.NewCol(0.22, gpuInfoPara),
			),
			ui.NewRow(0.27,
				ui.NewCol(0.50, gpuBar),
				ui.NewCol(0.50, memBar),
			),
			ui.NewRow(0.28,
				ui.NewCol(0.50,
					ui.NewRow(0.5, netInfo),
					ui.NewRow(0.5, netConns),
				),
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
		wifiHint := ""
		if wifiAvailable {
			wifiHint = "  [<w>](fg:green,modifier:bold) [wi-fi](fg:green)"
		}
		header.Text = fmt.Sprintf(
			" [$](fg:green,modifier:bold) "+
				"[host](fg:green)=[%s](fg:green,modifier:bold)  "+
				"[time](fg:green)=[%s](fg:green,modifier:bold)  "+
				"[local](fg:green)=[%s](fg:green,modifier:bold)  "+
				"[public](fg:green)=[%s](fg:green,modifier:bold)%s   "+
				"[<q>](fg:red,modifier:bold) [exit](fg:red)",
			hostname,
			time.Now().Format("2006-01-02 15:04:05"),
			local, public, wifiHint,
		)

		// ── CPU ───────────────────────────────────────────────────────────────
		cpu, err := CollectCPU()
		if err == nil && cpu != nil {
			// Per-core load bars (same visual style as GPU power gauge),
			// laid out as `cpuColumns` evenly-sized columns filled
			// column-wise:  column 0 = cores 0..rows-1, column 1 = cores
			// rows..2*rows-1, etc. So a 30-core system shows 10 rows × 3
			// columns; a 20-core system shows 7 rows × 3 columns with one
			// trailing empty cell. Cell width and bar length adapt to
			// the current panel size.
			var sb strings.Builder
			n := len(cpu.CoreUsages)
			rows := (n + cpuColumns - 1) / cpuColumns // ceil(n / cpuColumns)
			idxWidth := 2
			if n > 99 {
				idxWidth = 3
			}
			panelW := cpuCores.Inner.Dx()
			if panelW <= 0 {
				panelW = 60
			}
			colW := panelW / cpuColumns
			// Cell layout:  Cxx ' ' bar ' ' xxx%   (with trailing pad)
			fixedW := idxWidth + cpuCellPercent + 2
			barW := colW - fixedW
			if barW < cpuBarMin {
				barW = cpuBarMin
			}
			if barW > cpuBarMax {
				barW = cpuBarMax
			}
			cellVisualW := idxWidth + 1 + barW + 1 + 4
			labelFmt := fmt.Sprintf("[C%%0%dd](fg:green,modifier:bold) ", idxWidth)
			for r := 0; r < rows; r++ {
				for c := 0; c < cpuColumns; c++ {
					idx := c*rows + r
					if idx >= n {
						// Trailing empty cell — pad with spaces so
						// neighboring columns stay aligned.
						sb.WriteString(strings.Repeat(" ", colW))
						continue
					}
					u := cpu.CoreUsages[idx]
					color := colorForPct(u)
					sb.WriteString(fmt.Sprintf(labelFmt, idx))
					sb.WriteString(fmt.Sprintf("[%s](%s) ", loadBar(u, barW), color))
					sb.WriteString(fmt.Sprintf("[%3.0f%%](%s)", u, color))
					if pad := colW - cellVisualW; pad > 0 {
						sb.WriteString(strings.Repeat(" ", pad))
					}
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

		if len(gpuHistory) >= 1 {
			n := visibleBarCount(gpuBar.Inner.Dx(), gpuBar.BarWidth, gpuBar.BarGap)
			data := tailBars(gpuHistory, n)
			gpuBar.Data = data
			gpuBar.Labels = make([]string, len(data))
		}
		gpuBar.Title = fmt.Sprintf(
			" gpu.compute.history  cur: %.0f%%  peak: %.0f%%  range: 0–100%% ",
			currentGPU, peakGPU,
		)

		if len(memHistory) >= 1 {
			n := visibleBarCount(memBar.Inner.Dx(), memBar.BarWidth, memBar.BarGap)
			data := tailBars(memHistory, n)
			memBar.Data = data
			memBar.Labels = make([]string, len(data))
		}
		if totalMemGiB > 0 {
			memBar.MaxVal = totalMemGiB
		}
		memBar.Title = fmt.Sprintf(
			" mem.unified.history  cur: %.1f  peak: %.1f  total: %.1f GiB ",
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

		// Plot expects Data[series][sample]. Series 0 = RX, series 1 = TX.
		// Plot panics if a series has fewer than 2 points, so we hold a
		// minimum of two samples per line.
		if len(rxHistory) >= 1 && len(txHistory) >= 1 {
			plotW := netPlot.Inner.Dx() - 6 // reserve space for axes
			if plotW < 2 {
				plotW = 2
			}
			rx := tailBars(rxHistory, plotW)
			tx := tailBars(txHistory, plotW)
			cnt := len(rx)
			if len(tx) < cnt {
				cnt = len(tx)
			}
			rx = rx[len(rx)-cnt:]
			tx = tx[len(tx)-cnt:]
			if cnt < 2 {
				rx = append([]float64{0}, rx...)
				tx = append([]float64{0}, tx...)
			}
			netPlot.Data = [][]float64{rx, tx}
			var maxV float64
			for _, v := range rx {
				if v > maxV {
					maxV = v
				}
			}
			for _, v := range tx {
				if v > maxV {
					maxV = v
				}
			}
			if maxV > 0 {
				netPlot.MaxVal = maxV * 1.1
			} else {
				netPlot.MaxVal = 1
			}
		}
		netPlot.Title = fmt.Sprintf(
			" net.bandwidth  ↓ %s (peak %s)  ↑ %s (peak %s) ",
			formatBps(currentRxBps), formatBps(peakRxBps),
			formatBps(currentTxBps), formatBps(peakTxBps),
		)

		// ── Storage ───────────────────────────────────────────────────────────
		// Re-enumerate block devices at most once every
		// storageRefreshRate; the data changes rarely and statfs() can
		// stall on a flaky drive. The render layer still runs every
		// tick so the bar resizes with the panel.
		if storageDevs == nil || time.Since(storageLast) >= storageRefreshRate {
			storageDevs = CollectStorage()
			storageLast = time.Now()
		}
		storagePara.Text = renderStoragePanel(storageDevs, storagePara.Inner.Dx())

		// ── Active TCP connections ────────────────────────────────────────────
		conns := CollectConnections()
		// Cap rows to what the panel can show to avoid clipping. Reserve
		// 2 rows for borders and a header line.
		maxRows := netConns.Inner.Dy() - 1
		if maxRows < 2 {
			maxRows = 2
		}
		var cb strings.Builder
		cb.WriteString(fmt.Sprintf(" [→ %d active](fg:green,modifier:bold)\n", len(conns)))
		shown := 0
		for _, c := range conns {
			if shown >= maxRows-1 {
				break
			}
			host := ResolveAsync(c.RemoteIP.String())
			if host == "" {
				host = c.RemoteIP.String()
			}
			// Direction heuristic: if we're listening on a low local port,
			// the remote initiated — call it incoming. Otherwise outgoing.
			arrow := "[→](fg:green,modifier:bold)"
			if c.LocalPort > 0 && c.LocalPort < 1024 {
				arrow = "[←](fg:red,modifier:bold)"
			}
			cb.WriteString(fmt.Sprintf(" %s %-38s %s\n",
				arrow, truncate(host, 38), c.Remote))
			shown++
		}
		if len(conns) == 0 {
			cb.WriteString(" [(no established connections)](fg:white)")
		}
		netConns.Text = cb.String()

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
			if wifi.Active() {
				// Resize is the only event we handle specially while modal is up.
				if e.ID == "<Resize>" {
					setGrid()
					ui.Clear()
					wifi.Render()
					continue
				}
				if wifi.HandleEvent(e) {
					if wifi.Active() {
						ui.Clear()
						wifi.Render()
					} else {
						// Modal just closed — repaint dashboard.
						ui.Clear()
						update()
					}
				}
				continue
			}
			switch e.ID {
			case "q", "<C-c>":
				return
			case "w", "W":
				if wifiAvailable && wifi.Open() {
					ui.Clear()
					wifi.Render()
				}
			case "<Resize>":
				setGrid()
				ui.Clear()
				ui.Render(grid)
			}
		case res := <-wifi.OpCh:
			wifi.HandleResult(res)
			if wifi.Active() {
				ui.Clear()
				wifi.Render()
			}
		case <-ticker.C:
			// Suspend dashboard repaints while the modal owns the screen.
			if !wifi.Active() {
				update()
			}
		}
	}
}
