package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// WifiNetwork describes one network visible to a wireless interface.
type WifiNetwork struct {
	SSID     string
	Signal   int    // 0–100
	Security string // e.g. "WPA2", "WPA3", "WPA2 802.1X", or "" for open
	InUse    bool
}

// IsOpen reports whether the network has no security (no password required).
func (n WifiNetwork) IsOpen() bool {
	s := strings.TrimSpace(n.Security)
	return s == "" || s == "--"
}

// HasNmcli reports whether nmcli is available on PATH.
func HasNmcli() bool {
	_, err := exec.LookPath("nmcli")
	return err == nil
}

// WifiInterfaces returns the list of wireless device names known to
// NetworkManager, or nil if nmcli is unavailable.
func WifiInterfaces() []string {
	if !HasNmcli() {
		return nil
	}
	out, err := exec.Command("nmcli", "-t", "-f", "DEVICE,TYPE", "device").Output()
	if err != nil {
		return nil
	}
	var ifaces []string
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		parts := splitNmcliLine(sc.Text())
		if len(parts) >= 2 && parts[1] == "wifi" {
			ifaces = append(ifaces, parts[0])
		}
	}
	return ifaces
}

// WifiScan triggers a rescan and returns visible networks for iface,
// sorted strongest signal first, deduplicated by SSID.
func WifiScan(iface string) ([]WifiNetwork, error) {
	// Best-effort rescan. nmcli rejects rapid rescans; ignoring the error
	// just means we read the cached list.
	_ = exec.Command("nmcli", "device", "wifi", "rescan", "ifname", iface).Run()

	out, err := exec.Command("nmcli", "-t", "-f", "IN-USE,SSID,SIGNAL,SECURITY",
		"device", "wifi", "list", "ifname", iface).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nmcli scan failed: %s", strings.TrimSpace(string(out)))
	}

	seen := map[string]bool{}
	var nets []WifiNetwork
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		parts := splitNmcliLine(sc.Text())
		if len(parts) < 4 {
			continue
		}
		ssid := parts[1]
		if ssid == "" || seen[ssid] {
			continue
		}
		seen[ssid] = true
		sig, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
		nets = append(nets, WifiNetwork{
			InUse:    parts[0] == "*",
			SSID:     ssid,
			Signal:   sig,
			Security: strings.TrimSpace(parts[3]),
		})
	}
	sort.SliceStable(nets, func(i, j int) bool { return nets[i].Signal > nets[j].Signal })
	return nets, nil
}

// WifiConnect attempts to associate iface with ssid, optionally with a password.
// Only the named iface is touched; other interfaces are unaffected.
func WifiConnect(iface, ssid, password string) error {
	args := []string{"device", "wifi", "connect", ssid, "ifname", iface}
	if password != "" {
		args = append(args, "password", password)
	}
	out, err := exec.Command("nmcli", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// WifiDisconnect drops only iface; no other interface is altered.
func WifiDisconnect(iface string) error {
	out, err := exec.Command("nmcli", "device", "disconnect", iface).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// WifiReconnect reactivates the most recent connection on iface.
func WifiReconnect(iface string) error {
	out, err := exec.Command("nmcli", "device", "connect", iface).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// WifiRadioState turns the global Wi-Fi radio on or off (affects only Wi-Fi).
func WifiRadioState(on bool) error {
	arg := "off"
	if on {
		arg = "on"
	}
	out, err := exec.Command("nmcli", "radio", "wifi", arg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// splitNmcliLine splits a `nmcli -t` output line on unescaped colons,
// honoring the `\:` escape sequence used inside SSIDs.
func splitNmcliLine(line string) []string {
	var parts []string
	var cur strings.Builder
	esc := false
	for _, r := range line {
		if esc {
			cur.WriteRune(r)
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		if r == ':' {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	parts = append(parts, cur.String())
	return parts
}
