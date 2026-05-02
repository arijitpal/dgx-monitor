package main

import (
	"bufio"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NetConnection is one row of /proc/net/tcp[6] after parsing.
type NetConnection struct {
	State     string // ESTAB, LISTEN, etc.
	LocalPort int
	Remote    string // "ip:port" — display form
	RemoteIP  net.IP
	IsIPv6    bool
}

// procNetTCPState maps the hex state field used in /proc/net/tcp[6].
var procNetTCPState = map[string]string{
	"01": "ESTAB",
	"02": "SYN_SENT",
	"03": "SYN_RECV",
	"04": "FIN_WAIT1",
	"05": "FIN_WAIT2",
	"06": "TIME_WAIT",
	"07": "CLOSE",
	"08": "CLOSE_WAIT",
	"09": "LAST_ACK",
	"0A": "LISTEN",
	"0B": "CLOSING",
}

// CollectConnections returns the current ESTABLISHED TCP connections
// (IPv4 + IPv6) with non-loopback, non-unspecified remote endpoints,
// deduplicated by remote address. The list is sorted by remote
// (then by hostname when available) for stable display.
func CollectConnections() []NetConnection {
	var conns []NetConnection
	conns = append(conns, parseProcNet("/proc/net/tcp", false)...)
	conns = append(conns, parseProcNet("/proc/net/tcp6", true)...)

	out := conns[:0]
	seen := map[string]bool{}
	for _, c := range conns {
		if c.State != "ESTAB" {
			continue
		}
		if c.RemoteIP == nil || c.RemoteIP.IsLoopback() || c.RemoteIP.IsUnspecified() || c.RemoteIP.IsLinkLocalUnicast() {
			continue
		}
		if seen[c.Remote] {
			continue
		}
		seen[c.Remote] = true
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Remote < out[j].Remote })
	return out
}

func parseProcNet(path string, ipv6 bool) []NetConnection {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []NetConnection
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		// 0:sl 1:local 2:remote 3:state ...
		if len(fields) < 4 {
			continue
		}
		stateName, ok := procNetTCPState[fields[3]]
		if !ok {
			continue
		}
		localIP, localPort := parseHexEndpoint(fields[1], ipv6)
		remoteIP, remotePort := parseHexEndpoint(fields[2], ipv6)
		if remoteIP == nil {
			continue
		}
		_ = localIP
		out = append(out, NetConnection{
			State:     stateName,
			LocalPort: localPort,
			RemoteIP:  remoteIP,
			Remote:    formatEndpoint(remoteIP, remotePort),
			IsIPv6:    ipv6,
		})
	}
	return out
}

func parseHexEndpoint(s string, ipv6 bool) (net.IP, int) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return nil, 0
	}
	port, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return nil, 0
	}
	hexAddr := parts[0]
	if ipv6 {
		return parseHexIPv6(hexAddr), int(port)
	}
	return parseHexIPv4(hexAddr), int(port)
}

// /proc/net/tcp encodes the IPv4 address as a 32-bit integer in host byte
// order, formatted as 8 hex characters. To get the dotted form we read
// the bytes and reverse them (Linux runs little-endian on every platform
// we care about; the kernel still emits the bytes in this order).
func parseHexIPv4(s string) net.IP {
	if len(s) != 8 {
		return nil
	}
	b := make([]byte, 4)
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseUint(s[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil
		}
		b[3-i] = byte(v)
	}
	return net.IPv4(b[0], b[1], b[2], b[3]).To4()
}

// IPv6 follows the same pattern but in four 32-bit little-endian groups
// concatenated into 16 bytes.
func parseHexIPv6(s string) net.IP {
	if len(s) != 32 {
		return nil
	}
	b := make([]byte, 16)
	for g := 0; g < 4; g++ {
		for i := 0; i < 4; i++ {
			v, err := strconv.ParseUint(s[g*8+i*2:g*8+i*2+2], 16, 8)
			if err != nil {
				return nil
			}
			b[g*4+(3-i)] = byte(v)
		}
	}
	return net.IP(b)
}

func formatEndpoint(ip net.IP, port int) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String() + ":" + strconv.Itoa(port)
	}
	return "[" + ip.String() + "]:" + strconv.Itoa(port)
}

// ── DNS resolution (async + cached) ──────────────────────────────────────────

// reverseDNS keeps a small cache of IP → first PTR name. A miss kicks off
// an async lookup; the UI loop never blocks on DNS.
var (
	dnsCache      sync.Map // map[string]dnsEntry
	dnsInProgress sync.Map // map[string]struct{}
)

type dnsEntry struct {
	name string
	ts   time.Time
}

const dnsTTL = 5 * time.Minute

// ResolveAsync returns the cached PTR name for ip, or "" if not yet
// resolved. On miss, schedules a background lookup whose result will be
// available on subsequent calls.
func ResolveAsync(ip string) string {
	if v, ok := dnsCache.Load(ip); ok {
		e := v.(dnsEntry)
		if time.Since(e.ts) < dnsTTL {
			return e.name
		}
	}
	if _, busy := dnsInProgress.LoadOrStore(ip, struct{}{}); busy {
		return ""
	}
	go func() {
		defer dnsInProgress.Delete(ip)
		// Bounded resolver; LookupAddr respects /etc/resolv.conf timeouts.
		names, err := net.LookupAddr(ip)
		name := ""
		if err == nil && len(names) > 0 {
			name = strings.TrimSuffix(names[0], ".")
		}
		dnsCache.Store(ip, dnsEntry{name: name, ts: time.Now()})
	}()
	return ""
}
