# dgx-monitor

A terminal-based real-time monitoring dashboard for NVIDIA DGX GB-10 systems with unified memory (e.g. DGX Spark). Displays CPU, GPU, memory, power, and thermal metrics in a single TUI view — no external services required.

---

## Requirements

- Linux (reads `/proc/stat`, `/proc/meminfo`, `/proc/net/dev`, `/sys/devices/system/cpu/`, `/sys/class/net/*/`)
- Go 1.21+
- NVIDIA driver with NVML support (for GPU metrics)
- A terminal at least 120 columns × 40 rows for best layout

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
│ ░▒▓ DGX SPARK MONITOR ▓▒░  $ host=…  time=…  local=…  public=…  <q>│
├──────────────────────────────────┬─────────────┬────────────────────┤
│  cpu.cores  (column layout)      │ cpu.avg     │ cpu.freq           │
│                                  │ gauge       │ avg / max / min    │
├──────────────┬───────────────────┼─────────────┴──────────┬─────────┤
│ gpu.compute  │ mem.unified       │ gpu.power (0–140 W)    │gpu.info │
├──────────────┴───────────────────┴────────────────────────┴─────────┤
│ gpu.compute.history (2D line)    │ mem.unified.history (2D line)   │
│ % vs time, right-to-left         │ GiB vs time, right-to-left      │
├──────────────────────────────────┴──────────────────────────────────┤
│ net.interfaces (kinds: wifi/eth  │ net.bandwidth (RX green / TX    │
│ first, then virt/loop)           │ red, right-to-left)             │
└──────────────────────────────────┴──────────────────────────────────┘
```

The whole dashboard uses a green-on-black "matrix terminal" aesthetic:
green borders, bold green titles, RX traffic in green and TX in red.
Threshold colors (yellow / red) are still used on gauges and link
states for at-a-glance warnings.

---

## Panels

### Header
- **Host** — system hostname
- **Time** — current local time (1 s resolution)
- **Local** — first non-loopback IPv4 address bound to an active interface (physical/wifi NICs preferred over virtual ones)
- **Public** — public IP fetched from `https://api.ipify.org` (refreshed every 5 minutes in the background; shows `—` if unavailable / no internet)

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

### Network Interfaces
- One line per network interface detected in `/proc/net/dev`
- **Status indicator:**
  - `●` green — `ACTIVE` (operstate `up` and carrier detected)
  - `●` yellow — `NO LINK` (operstate `up` but no carrier — e.g. unplugged cable)
  - `○` red — `INACTIVE` (operstate `down`)
- **Kind label** (color-coded):
  - `wifi` (magenta) — wireless device (has `/sys/class/net/<iface>/wireless` or `/phy80211`)
  - `eth ` (cyan) — physical NIC (has `/sys/class/net/<iface>/device` symlink)
  - `virt` (white) — virtual device (docker, bridge, veth, tun/tap, virbr, etc.)
  - `loop` (white) — loopback
- **Bandwidth:** per-interface RX (`↓`) and TX (`↑`) rate, auto-scaled `B/s` → `KB/s` → `MB/s` → `GB/s`
- **Sort order:** real wifi/ethernet interfaces always at the top, then virtual, then loopback. Within each kind: active+link → up but no link → down, then alphabetical.
- **Sources:** `/proc/net/dev` (counters), `/sys/class/net/<iface>/operstate` (state), `/sys/class/net/<iface>/carrier` (link), `/sys/class/net/<iface>/{wireless,phy80211,device}` (kind detection)

### Network Bandwidth Graph
- 2D line chart with two series:
  - **Green** — total RX (incoming) bytes/sec
  - **Red** — total TX (outgoing) bytes/sec
- 120-second rolling window, scrolls right-to-left
- Y-axis auto-scales to the highest observed throughput
- Loopback (`lo`) is excluded from the totals
- Title: `↓ <cur> (peak <peak>)  ↑ <cur> (peak <peak>)`

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
