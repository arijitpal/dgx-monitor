package main

import (
	"fmt"
	"strings"

	ui "github.com/gizak/termui/v3"
	"github.com/gizak/termui/v3/widgets"
)

// wifiMode tracks which sub-screen of the wi-fi modal is active.
type wifiMode int

const (
	wifiOff wifiMode = iota
	wifiPickIface
	wifiAction
	wifiList
	wifiPassword
	wifiBusy
	wifiResult
)

// wifiOpResult is sent on opCh when an async nmcli operation completes.
type wifiOpResult struct {
	op       string
	err      error
	networks []WifiNetwork
}

// wifiUI is a self-contained modal that overlays the dashboard.
// All nmcli calls happen in goroutines; results flow back through OpCh
// so the UI thread never blocks.
type wifiUI struct {
	mode       wifiMode
	interfaces []string
	iface      string
	networks   []WifiNetwork
	password   []rune
	pendingNet WifiNetwork
	msg        string
	msgErr     bool

	list   *widgets.List
	prompt *widgets.Paragraph

	OpCh chan wifiOpResult
}

func newWifiUI() *wifiUI {
	greenBorder := ui.NewStyle(ui.ColorGreen)
	greenBoldTitle := ui.NewStyle(ui.ColorGreen, ui.ColorClear, ui.ModifierBold)
	greenText := ui.NewStyle(ui.ColorGreen)

	list := widgets.NewList()
	list.BorderStyle = greenBorder
	list.TitleStyle = greenBoldTitle
	list.TextStyle = greenText
	list.SelectedRowStyle = ui.NewStyle(ui.ColorBlack, ui.ColorGreen, ui.ModifierBold)
	list.WrapText = false

	prompt := widgets.NewParagraph()
	prompt.BorderStyle = greenBorder
	prompt.TitleStyle = greenBoldTitle
	prompt.TextStyle = greenText
	prompt.PaddingLeft = 1
	prompt.PaddingTop = 0

	return &wifiUI{
		list:   list,
		prompt: prompt,
		OpCh:   make(chan wifiOpResult, 4),
	}
}

// Active reports whether the modal currently owns the screen.
func (w *wifiUI) Active() bool { return w.mode != wifiOff }

// Open enters the modal; returns false if no wi-fi interface was found.
func (w *wifiUI) Open() bool {
	ifaces := WifiInterfaces()
	if len(ifaces) == 0 {
		return false
	}
	w.interfaces = ifaces
	w.password = nil
	w.msg = ""
	w.msgErr = false
	if len(ifaces) == 1 {
		w.iface = ifaces[0]
		w.enterAction()
	} else {
		w.iface = ""
		w.enterPickIface()
	}
	return true
}

func (w *wifiUI) Close() { w.mode = wifiOff }

// ── Mode transitions ─────────────────────────────────────────────────────────

func (w *wifiUI) enterPickIface() {
	w.mode = wifiPickIface
	rows := make([]string, len(w.interfaces))
	for i, n := range w.interfaces {
		rows[i] = " " + n
	}
	w.list.Rows = rows
	w.list.SelectedRow = 0
}

func (w *wifiUI) enterAction() {
	w.mode = wifiAction
	w.list.Rows = []string{
		" Scan & connect to network",
		" Disconnect this interface",
		" Reconnect this interface",
		" Turn Wi-Fi radio ON",
		" Turn Wi-Fi radio OFF",
	}
	w.list.SelectedRow = 0
}

func (w *wifiUI) enterList() {
	w.mode = wifiList
	rows := make([]string, len(w.networks))
	for i, n := range w.networks {
		mark := " "
		if n.InUse {
			mark = "*"
		}
		sec := n.Security
		if n.IsOpen() {
			sec = "open"
		}
		rows[i] = fmt.Sprintf(" %s  %-32s  %s  %3d%%  %s",
			mark, truncate(n.SSID, 32), signalBars(n.Signal), n.Signal, sec)
	}
	if len(rows) == 0 {
		rows = []string{" (no networks found — press r to rescan, esc back)"}
	}
	w.list.Rows = rows
	w.list.SelectedRow = 0
}

func (w *wifiUI) enterPassword(n WifiNetwork) {
	w.mode = wifiPassword
	w.pendingNet = n
	w.password = nil
}

func (w *wifiUI) enterBusy(msg string) {
	w.mode = wifiBusy
	w.msg = msg
}

func (w *wifiUI) enterResult(msg string, isErr bool) {
	w.mode = wifiResult
	w.msg = msg
	w.msgErr = isErr
}

// ── Render ───────────────────────────────────────────────────────────────────

// Render draws the modal centered on the screen. Caller must ui.Clear() first
// when first entering or transitioning.
func (w *wifiUI) Render() {
	tw, th := ui.TerminalDimensions()
	width := 80
	if width > tw-4 {
		width = tw - 4
	}
	height := 24
	if height > th-4 {
		height = th - 4
	}
	x := (tw - width) / 2
	y := (th - height) / 2

	switch w.mode {
	case wifiPickIface:
		w.list.Title = " select wi-fi interface  (↑↓ select · enter confirm · esc cancel) "
		w.list.SetRect(x, y, x+width, y+height)
		ui.Render(w.list)

	case wifiAction:
		w.list.Title = fmt.Sprintf(" wi-fi: %s  (↑↓ select · enter · esc back) ", w.iface)
		w.list.SetRect(x, y, x+width, y+height)
		ui.Render(w.list)

	case wifiList:
		w.list.Title = fmt.Sprintf(" networks on %s  (↑↓ · enter · r rescan · esc back) ", w.iface)
		w.list.SetRect(x, y, x+width, y+height)
		ui.Render(w.list)

	case wifiPassword:
		w.prompt.Title = fmt.Sprintf(" password for %q  (enter · esc back) ", w.pendingNet.SSID)
		masked := strings.Repeat("•", len(w.password))
		w.prompt.Text = "\n  password: [" + masked + "_](fg:green,modifier:bold)\n\n  security: " + w.pendingNet.Security
		ph := 8
		w.prompt.SetRect(x, y+(height-ph)/2, x+width, y+(height-ph)/2+ph)
		ui.Render(w.prompt)

	case wifiBusy:
		w.prompt.Title = " wi-fi "
		w.prompt.TitleStyle = ui.NewStyle(ui.ColorGreen, ui.ColorClear, ui.ModifierBold)
		w.prompt.BorderStyle = ui.NewStyle(ui.ColorGreen)
		w.prompt.Text = "\n  " + w.msg + "\n"
		ph := 5
		w.prompt.SetRect(x, y+(height-ph)/2, x+width, y+(height-ph)/2+ph)
		ui.Render(w.prompt)

	case wifiResult:
		if w.msgErr {
			w.prompt.Title = " ✗ failed  (any key continues · q exit) "
			w.prompt.TitleStyle = ui.NewStyle(ui.ColorRed, ui.ColorClear, ui.ModifierBold)
			w.prompt.BorderStyle = ui.NewStyle(ui.ColorRed)
		} else {
			w.prompt.Title = " ✓ success  (any key continues · q exit) "
			w.prompt.TitleStyle = ui.NewStyle(ui.ColorGreen, ui.ColorClear, ui.ModifierBold)
			w.prompt.BorderStyle = ui.NewStyle(ui.ColorGreen)
		}
		w.prompt.Text = "\n  " + w.msg + "\n"
		// Auto-size the panel to fit multi-line hints (e.g. polkit guidance).
		ph := 5 + strings.Count(w.msg, "\n")
		if ph < 6 {
			ph = 6
		}
		if ph > height {
			ph = height
		}
		w.prompt.SetRect(x, y+(height-ph)/2, x+width, y+(height-ph)/2+ph)
		ui.Render(w.prompt)
	}
}

// ── Event handling ───────────────────────────────────────────────────────────

// HandleEvent processes a key event. Returns true if the modal should re-render.
func (w *wifiUI) HandleEvent(e ui.Event) bool {
	switch w.mode {
	case wifiPickIface:
		switch e.ID {
		case "<Escape>", "q":
			w.Close()
		case "<Up>":
			w.list.ScrollUp()
		case "<Down>":
			w.list.ScrollDown()
		case "<Enter>":
			if w.list.SelectedRow < len(w.interfaces) {
				w.iface = w.interfaces[w.list.SelectedRow]
				w.enterAction()
			}
		}

	case wifiAction:
		switch e.ID {
		case "<Escape>":
			if len(w.interfaces) > 1 {
				w.enterPickIface()
			} else {
				w.Close()
			}
		case "q":
			w.Close()
		case "<Up>":
			w.list.ScrollUp()
		case "<Down>":
			w.list.ScrollDown()
		case "<Enter>":
			w.runAction(w.list.SelectedRow)
		}

	case wifiList:
		switch e.ID {
		case "<Escape>":
			w.enterAction()
		case "<Up>":
			w.list.ScrollUp()
		case "<Down>":
			w.list.ScrollDown()
		case "r", "R":
			w.beginScan()
		case "<Enter>":
			if len(w.networks) == 0 {
				return true
			}
			n := w.networks[w.list.SelectedRow]
			if n.IsOpen() {
				w.beginConnect(n, "")
			} else {
				w.enterPassword(n)
			}
		}

	case wifiPassword:
		switch e.ID {
		case "<Escape>":
			w.password = nil
			w.enterList()
		case "<Enter>":
			w.beginConnect(w.pendingNet, string(w.password))
		case "<Backspace>", "<C-h>", "<Backspace2>":
			if len(w.password) > 0 {
				w.password = w.password[:len(w.password)-1]
			}
		case "<Space>":
			w.password = append(w.password, ' ')
		default:
			// Treat any other un-bracketed event as a printable character.
			if e.ID != "" && !strings.HasPrefix(e.ID, "<") {
				for _, r := range e.ID {
					w.password = append(w.password, r)
				}
			}
		}

	case wifiBusy:
		// Ignore input while a goroutine is running. ESC could be wired
		// to a cancel context later; nmcli operations are fast enough
		// (<5 s) that the simpler "wait it out" approach is fine.

	case wifiResult:
		switch e.ID {
		case "<Escape>", "q":
			w.Close()
		default:
			w.enterAction()
		}

	default:
		return false
	}
	return true
}

// HandleResult applies an async op result to the modal state.
func (w *wifiUI) HandleResult(r wifiOpResult) {
	switch r.op {
	case "scan":
		if r.err != nil {
			w.enterResult("Scan failed: "+formatNmcliErr(r.err), true)
			return
		}
		w.networks = r.networks
		if len(w.networks) == 0 {
			w.enterResult("No networks visible to "+w.iface, true)
			return
		}
		w.enterList()

	case "connect":
		if r.err != nil {
			w.enterResult("Connect failed: "+formatNmcliErr(r.err), true)
		} else {
			w.enterResult("Connected to "+w.pendingNet.SSID+" on "+w.iface, false)
		}
		w.password = nil

	case "disconnect":
		if r.err != nil {
			w.enterResult("Disconnect failed: "+formatNmcliErr(r.err), true)
		} else {
			w.enterResult("Disconnected "+w.iface, false)
		}

	case "reconnect":
		if r.err != nil {
			w.enterResult("Reconnect failed: "+formatNmcliErr(r.err), true)
		} else {
			w.enterResult("Reconnected "+w.iface, false)
		}

	case "radio_on":
		if r.err != nil {
			w.enterResult("Failed to enable Wi-Fi radio: "+formatNmcliErr(r.err), true)
		} else {
			w.enterResult("Wi-Fi radio is ON", false)
		}

	case "radio_off":
		if r.err != nil {
			w.enterResult("Failed to disable Wi-Fi radio: "+formatNmcliErr(r.err), true)
		} else {
			w.enterResult("Wi-Fi radio is OFF", false)
		}
	}
}

// formatNmcliErr augments common nmcli error messages with actionable
// guidance — most importantly for polkit authorization failures, which
// can't be answered interactively from a TUI.
func formatNmcliErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "not authorized"),
		strings.Contains(low, "permission denied"),
		strings.Contains(low, "policykit"):
		return msg +
			"\n\n  this operation needs polkit / NetworkManager permission." +
			"\n  fixes (any one):" +
			"\n    • run with sudo:  sudo ./dgx-monitor" +
			"\n    • add user to the 'netdev' group, then log out / in" +
			"\n    • install a polkit rule allowing nmcli for your user"
	case strings.Contains(low, "secrets were required"),
		strings.Contains(low, "no secrets"):
		return msg + "\n\n  hint: the supplied password was rejected — try again."
	case strings.Contains(low, "no network with ssid"),
		strings.Contains(low, "ssid not found"):
		return msg + "\n\n  hint: SSID is no longer visible. Press r to rescan."
	default:
		return msg
	}
}

// ── Async ops ────────────────────────────────────────────────────────────────

func (w *wifiUI) runAction(idx int) {
	switch idx {
	case 0:
		w.beginScan()
	case 1:
		w.beginOp("disconnect", "Disconnecting "+w.iface+"…", func() error { return WifiDisconnect(w.iface) })
	case 2:
		w.beginOp("reconnect", "Reconnecting "+w.iface+"…", func() error { return WifiReconnect(w.iface) })
	case 3:
		w.beginOp("radio_on", "Turning Wi-Fi radio ON…", func() error { return WifiRadioState(true) })
	case 4:
		w.beginOp("radio_off", "Turning Wi-Fi radio OFF…", func() error { return WifiRadioState(false) })
	}
}

func (w *wifiUI) beginScan() {
	w.enterBusy("Scanning networks on " + w.iface + "…")
	iface := w.iface
	go func() {
		nets, err := WifiScan(iface)
		w.OpCh <- wifiOpResult{op: "scan", err: err, networks: nets}
	}()
}

func (w *wifiUI) beginConnect(n WifiNetwork, pwd string) {
	w.pendingNet = n
	w.enterBusy("Connecting to " + n.SSID + " on " + w.iface + "…")
	iface := w.iface
	ssid := n.SSID
	go func() {
		err := WifiConnect(iface, ssid, pwd)
		w.OpCh <- wifiOpResult{op: "connect", err: err}
	}()
}

func (w *wifiUI) beginOp(op, busyMsg string, fn func() error) {
	w.enterBusy(busyMsg)
	go func() {
		err := fn()
		w.OpCh <- wifiOpResult{op: op, err: err}
	}()
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// signalBars returns a 4-cell bar graph for a 0–100 signal strength.
func signalBars(pct int) string {
	switch {
	case pct >= 80:
		return "████"
	case pct >= 60:
		return "███▁"
	case pct >= 40:
		return "██▁▁"
	case pct >= 20:
		return "█▁▁▁"
	default:
		return "▁▁▁▁"
	}
}

// truncate clips s to at most n runes, appending an ellipsis if cut.
func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}
