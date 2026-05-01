package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// netSample stores raw byte counters from /proc/net/dev with a timestamp.
type netSample struct {
	rxBytes uint64
	txBytes uint64
	t       time.Time
}

// IfaceKind classifies an interface by its physical type.
type IfaceKind int

const (
	IfaceWireless IfaceKind = iota
	IfaceEthernet
	IfaceVirtual
	IfaceLoopback
)

func (k IfaceKind) Label() string {
	switch k {
	case IfaceWireless:
		return "wifi"
	case IfaceEthernet:
		return "eth "
	case IfaceLoopback:
		return "loop"
	default:
		return "virt"
	}
}

// NetIface holds the state and bandwidth for a single interface.
type NetIface struct {
	Name       string
	Kind       IfaceKind
	IsUp       bool    // operstate == "up"
	HasCarrier bool    // carrier == 1 (link detected)
	RxBps      float64 // bytes per second, received
	TxBps      float64 // bytes per second, transmitted
	RxTotal    uint64  // total bytes received since boot
	TxTotal    uint64  // total bytes transmitted since boot
}

// NetMetrics is the full snapshot returned by CollectNet.
type NetMetrics struct {
	Interfaces []NetIface
	TotalRxBps float64 // sum of all non-loopback RxBps
	TotalTxBps float64 // sum of all non-loopback TxBps
}

var prevNetSamples = map[string]netSample{}

func readNetDev() (map[string]netSample, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	samples := map[string]netSample{}
	now := time.Now()
	scanner := bufio.NewScanner(f)
	// /proc/net/dev has two header lines.
	scanner.Scan()
	scanner.Scan()
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		// Layout: rx_bytes, packets, errs, drop, fifo, frame, compressed, multicast,
		//         tx_bytes, packets, errs, drop, fifo, colls, carrier, compressed
		if len(fields) < 16 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		samples[name] = netSample{rxBytes: rx, txBytes: tx, t: now}
	}
	return samples, scanner.Err()
}

func ifaceStatus(name string) (isUp, carrier bool) {
	if data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/operstate", name)); err == nil {
		if strings.TrimSpace(string(data)) == "up" {
			isUp = true
		}
	}
	if data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/carrier", name)); err == nil {
		if strings.TrimSpace(string(data)) == "1" {
			carrier = true
		}
	}
	return
}

// ifaceKind decides whether an interface is wifi, physical ethernet,
// loopback, or a virtual device (docker, bridge, veth, tun/tap, etc.).
func ifaceKind(name string) IfaceKind {
	if name == "lo" {
		return IfaceLoopback
	}
	base := "/sys/class/net/" + name
	// Wireless interfaces expose a `wireless/` or `phy80211/` directory.
	if _, err := os.Stat(base + "/wireless"); err == nil {
		return IfaceWireless
	}
	if _, err := os.Stat(base + "/phy80211"); err == nil {
		return IfaceWireless
	}
	// A `device/` symlink means the kernel has a real driver-backed device
	// (physical Ethernet, USB Ethernet, Thunderbolt, etc.). Virtual
	// interfaces (docker0, br-*, veth*, tun*, virbr*) lack this.
	if _, err := os.Stat(base + "/device"); err == nil {
		return IfaceEthernet
	}
	return IfaceVirtual
}

// CollectNet returns the current bandwidth and link state for every interface.
// First call establishes a baseline; bandwidth is reported from the second call onward.
func CollectNet() (*NetMetrics, error) {
	curr, err := readNetDev()
	if err != nil {
		return nil, err
	}

	m := &NetMetrics{}
	for name, c := range curr {
		iface := NetIface{
			Name:    name,
			Kind:    ifaceKind(name),
			RxTotal: c.rxBytes,
			TxTotal: c.txBytes,
		}
		iface.IsUp, iface.HasCarrier = ifaceStatus(name)

		if p, ok := prevNetSamples[name]; ok {
			dt := c.t.Sub(p.t).Seconds()
			if dt > 0 {
				if c.rxBytes >= p.rxBytes {
					iface.RxBps = float64(c.rxBytes-p.rxBytes) / dt
				}
				if c.txBytes >= p.txBytes {
					iface.TxBps = float64(c.txBytes-p.txBytes) / dt
				}
			}
		}
		// Loopback is excluded from totals to keep the chart meaningful.
		if name != "lo" {
			m.TotalRxBps += iface.RxBps
			m.TotalTxBps += iface.TxBps
		}
		m.Interfaces = append(m.Interfaces, iface)
	}
	prevNetSamples = curr

	// Sort priority: real wifi/ethernet interfaces first (so the user's
	// actual NIC is always at the top), virtual devices next, loopback
	// last. Within each kind, active+link first, then up but no link,
	// then down. Alphabetical within each (kind, state) group.
	sort.SliceStable(m.Interfaces, func(i, j int) bool {
		a, b := m.Interfaces[i], m.Interfaces[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		ar := stateRank(a)
		br := stateRank(b)
		if ar != br {
			return ar < br
		}
		return a.Name < b.Name
	})

	return m, nil
}

func stateRank(i NetIface) int {
	switch {
	case i.IsUp && i.HasCarrier:
		return 0
	case i.IsUp:
		return 1
	default:
		return 2
	}
}

// formatBps formats a bytes/second value with auto-scaled units.
func formatBps(bps float64) string {
	units := []string{"B/s ", "KB/s", "MB/s", "GB/s"}
	val := bps
	i := 0
	for val >= 1024 && i < len(units)-1 {
		val /= 1024
		i++
	}
	return fmt.Sprintf("%7.1f %s", val, units[i])
}

// LocalIP returns the first non-loopback IPv4 address bound to an "up"
// interface, preferring physical/wifi NICs over virtual devices.
// Returns an empty string if nothing usable is found.
func LocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	type cand struct {
		ip   string
		kind IfaceKind
	}
	var cands []cand
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			cands = append(cands, cand{ip: ip4.String(), kind: ifaceKind(iface.Name)})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].kind < cands[j].kind })
	return cands[0].ip
}

// publicIPCache stores the most recently fetched public IP. atomic.Value
// keeps the read path lock-free for the UI render loop.
var publicIPCache atomic.Value // string

// PublicIP returns the cached public IP last fetched in the background
// (empty string until the first successful fetch completes).
func PublicIP() string {
	if v, ok := publicIPCache.Load().(string); ok {
		return v
	}
	return ""
}

// StartPublicIPRefresher fetches the public IP in the background and
// refreshes it periodically. Failures are silent — the cached value stays
// in place. Call once at startup.
func StartPublicIPRefresher() {
	publicIPCache.Store("")
	refresh := func() {
		client := http.Client{Timeout: 3 * time.Second}
		// api.ipify.org is plain text and very lightweight; if it fails
		// we just keep whatever was cached previously.
		resp, err := client.Get("https://api.ipify.org")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		if err != nil {
			return
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			publicIPCache.Store(ip)
		}
	}
	go func() {
		refresh()
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for range t.C {
			refresh()
		}
	}()
}
