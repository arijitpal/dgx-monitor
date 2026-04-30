# dgx-monitor

A terminal-based real-time monitoring dashboard for NVIDIA DGX GB-10 systems with unified memory (e.g. DGX Spark). Displays CPU, GPU, memory, power, and thermal metrics in a single TUI view — no external services required.

---

## Requirements

- Linux (reads `/proc/stat`, `/proc/meminfo`, `/sys/devices/system/cpu/`)
- Go 1.21+
- NVIDIA driver with NVML support (for GPU metrics)
- A terminal at least 120 columns × 36 rows for best layout

---

## Build

```bash
make          # runs go mod tidy + build
make build    # build only
make deps     # go mod tidy only
make clean    # remove the compiled binary
```

The binary is output to `./dgx-monitor`.

---

## Run

```bash
./dgx-monitor
```

No flags or arguments — the dashboard starts immediately and refreshes every second.

**Quit:** press `q` or `Ctrl-C`.

---

## Dashboard Layout

```
┌─────────────────────────────────────────────────────────────────────┐
│  DGX Spark Monitor   Host: <hostname>   Time: 2025-01-01 12:00:00   │
├──────────────────────────────────┬─────────────┬────────────────────┤
│  CPU Cores %                     │ CPU Avg     │ CPU Frequency      │
│  (per-core % in column layout)   │ Usage gauge │ Avg / Max / Min    │
├──────────────────┬───────────────┼─────────────┴──────────┬─────────┤
│  GPU Compute     │ Unified Mem   │  GPU Power             │GPU Info │
│  gauge           │ gauge (GiB)   │  gauge (0–140 W)       │         │
├──────────────────┴───────────────┴────────────────────────┴─────────┤
│  GPU Compute % — 2D line graph (2 min rolling, right-to-left)        │
│  Title shows: cur / peak / range 0–100%                              │
├─────────────────────────────────────────────────────────────────────┤
│  Unified Memory GiB — 2D line graph (2 min rolling, right-to-left)  │
│  Title shows: cur GiB / peak GiB / total GiB                        │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Panels

### Header
- **Host** — system hostname
- **Time** — current local time (1 s resolution)

### CPU Cores %
- Shows every logical core's current utilisation percentage
- Layout is **column-wise**: core 0 at top of column 1, core N at top of column 2, etc. (8 cores per column)
- Color coding: green < 50 %, yellow 50–80 %, red > 80 %
- Core labels: `C00`–`C99` for systems with < 100 cores; `C000`+ for ≥ 100 cores

### CPU Avg Usage
- Gauge showing the average utilisation across all logical cores
- Color matches load level (green / yellow / red)

### CPU Frequency
- Average, max, and min clock frequency across all cores (MHz)
- Total logical core count

### GPU Compute
- Current GPU compute utilisation (%)
- Label shows utilisation and current GPU clock (MHz)

### Unified Memory (gauge)
- System RAM utilisation gauge (maps to DGX Spark's unified memory)
- Label shows `XX%   used / total GiB`

### GPU Power
- Power draw as a gauge scaled to **140 W** max
- Label shows `current W / 140 W`
- Color: green < 50 %, yellow 50–80 %, red > 80 %

### GPU Info
- GPU model name
- GPU temperature (°C)
- Memory clock (MHz)
- VRAM used (GiB)

### GPU Compute % Graph
- 2D line chart, 120-second rolling window
- Scrolls right-to-left (newest sample on the right)
- Y-axis fixed 0–100 %
- Title: `cur: XX%  peak: XX%  range: 0–100%`

### Unified Memory GiB Graph
- 2D line chart, 120-second rolling window
- Scrolls right-to-left (newest sample on the right)
- Y-axis scaled to total memory (GiB)
- Title: `cur: XX.X  peak: XX.X  total: XX.X` (all in GiB)

---

## Dependencies

| Package | Purpose |
|---|---|
| `github.com/gizak/termui/v3` | Terminal UI widgets and grid layout |
| `github.com/NVIDIA/go-nvml` | NVML bindings for GPU metrics |

GPU metrics (compute utilisation, power, clocks, temperature, VRAM) require a compatible NVIDIA driver. If NVML initialisation fails, all GPU panels show `N/A — no NVML device` and the CPU/memory panels continue normally.

---

## Keyboard Controls

| Key | Action |
|---|---|
| `q` | Quit |
| `Ctrl-C` | Quit |

---

## License

See [LICENSE](LICENSE).
