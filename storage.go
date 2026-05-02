package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// StoragePartition is a single mounted volume that belongs to a parent
// block device.  When `HasUsage` is false we still record the kernel
// name so the UI can show "locked / no mount" without disappearing it.
type StoragePartition struct {
	Name        string // kernel name (e.g. "sda1") or mapper name for unlocked LUKS
	MountPoint  string
	UsedBytes   uint64
	TotalBytes  uint64
	UsedPercent float64
	HasUsage    bool
}

// StorageDevice describes a single physical block device and the
// partitions / volumes that sit on it.
type StorageDevice struct {
	Name         string // kernel name, e.g. "nvme0n1", "sda"
	Model        string // /sys/block/<dev>/device/model (if present)
	IsRemovable  bool   // /sys/block/<dev>/removable == 1
	IsUSB        bool   // device sits on a USB bus
	IsRotational bool   // 1 = HDD, 0 = SSD/NVMe
	Partitions   []StoragePartition
	Status       string // device-level status when no partitions could be inspected
}

// CollectStorage enumerates physical block devices, gathers every
// mounted partition (including dm/LUKS holders), and sorts internal
// drives ahead of removable / USB ones.
func CollectStorage() []StorageDevice {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	mounts := readMounts()

	var devs []StorageDevice
	for _, e := range entries {
		name := e.Name()
		if isPseudoBlockDevice(name) {
			continue
		}
		base := "/sys/block/" + name
		dev := StorageDevice{Name: name}

		if data, err := os.ReadFile(base + "/removable"); err == nil {
			dev.IsRemovable = strings.TrimSpace(string(data)) == "1"
		}
		if data, err := os.ReadFile(base + "/queue/rotational"); err == nil {
			dev.IsRotational = strings.TrimSpace(string(data)) == "1"
		}
		if target, err := filepath.EvalSymlinks(base); err == nil {
			if strings.Contains(target, "/usb") {
				dev.IsUSB = true
			}
		}
		if data, err := os.ReadFile(base + "/device/model"); err == nil {
			dev.Model = strings.TrimSpace(string(data))
		}

		dev.Partitions = collectPartitions(name, mounts)

		// Some devices expose no partition table — try the bare device.
		if len(dev.Partitions) == 0 {
			if mp, ok := mounts["/dev/"+name]; ok {
				if p := newPartitionFromMount(name, mp); p != nil {
					dev.Partitions = append(dev.Partitions, *p)
				}
			}
		}

		if len(dev.Partitions) == 0 {
			dev.Status = "no mount"
		}

		devs = append(devs, dev)
	}

	sort.SliceStable(devs, func(i, j int) bool {
		ai := devs[i].IsRemovable || devs[i].IsUSB
		aj := devs[j].IsRemovable || devs[j].IsUSB
		if ai != aj {
			return !ai
		}
		return devs[i].Name < devs[j].Name
	})
	return devs
}

func isPseudoBlockDevice(name string) bool {
	prefixes := []string{"loop", "ram", "dm-", "zram", "sr", "md", "mtdblock", "fd"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// readMounts maps device path → first mount point seen in /proc/mounts.
func readMounts() map[string]string {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		src := fields[0]
		if !strings.HasPrefix(src, "/dev/") {
			continue
		}
		if _, ok := out[src]; !ok {
			out[src] = fields[1]
		}
	}
	return out
}

// collectPartitions walks every partition directory under
// /sys/block/<dev>/ and records each mounted volume.  When a partition
// is fronted by a device-mapper (LUKS) holder, the holder's mount is
// reported instead so usage data stays meaningful.
func collectPartitions(dev string, mounts map[string]string) []StoragePartition {
	base := "/sys/block/" + dev
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}

	var parts []StoragePartition
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), dev) {
			continue
		}
		partName := e.Name()
		// Direct mount of the partition itself.
		if mp, ok := mounts["/dev/"+partName]; ok {
			if p := newPartitionFromMount(partName, mp); p != nil {
				parts = append(parts, *p)
				continue
			}
		}
		// Otherwise look for a holder (LUKS-unlocked dm device).
		if hp := holderPartition(base+"/"+partName, mounts); hp != nil {
			parts = append(parts, *hp)
		}
	}
	return parts
}

// holderPartition resolves the first mounted dm-holder of `partDir`.
// If a holder exists but isn't mounted, the partition is reported as
// "locked / no mount" so the UI can still show it.
func holderPartition(partDir string, mounts map[string]string) *StoragePartition {
	holdersDir := partDir + "/holders"
	hs, err := os.ReadDir(holdersDir)
	if err != nil || len(hs) == 0 {
		return nil
	}
	for _, h := range hs {
		// Prefer the friendlier mapper name when available.
		alt := h.Name()
		if data, err := os.ReadFile("/sys/block/" + h.Name() + "/dm/name"); err == nil {
			alt = strings.TrimSpace(string(data))
			if mp, ok := mounts["/dev/mapper/"+alt]; ok {
				if p := newPartitionFromMount(alt, mp); p != nil {
					return p
				}
			}
		}
		if mp, ok := mounts["/dev/"+h.Name()]; ok {
			if p := newPartitionFromMount(alt, mp); p != nil {
				return p
			}
		}
	}
	// Holder exists but no mount was found — report so the device line
	// still shows the volume rather than disappearing it.
	return &StoragePartition{Name: filepath.Base(partDir), HasUsage: false}
}

// newPartitionFromMount builds a StoragePartition by stat'ing the
// mount point.  Returns nil on failure (e.g. permission denied).
func newPartitionFromMount(name, mp string) *StoragePartition {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(mp, &stat); err != nil {
		return &StoragePartition{Name: name, MountPoint: mp, HasUsage: false}
	}
	total := stat.Blocks * uint64(stat.Bsize)
	avail := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return &StoragePartition{Name: name, MountPoint: mp, HasUsage: false}
	}
	used := total - avail
	return &StoragePartition{
		Name:        name,
		MountPoint:  mp,
		TotalBytes:  total,
		UsedBytes:   used,
		UsedPercent: float64(used) / float64(total) * 100.0,
		HasUsage:    true,
	}
}

// renderStoragePanel paints the device list — one header line per
// device, then one indented line per partition (mounted or not).
func renderStoragePanel(devs []StorageDevice, innerW int) string {
	if innerW <= 0 {
		innerW = 40
	}
	if len(devs) == 0 {
		return " [no storage devices](fg:white)"
	}
	var sb strings.Builder
	for _, d := range devs {
		kind := "internal"
		if d.IsUSB {
			kind = "usb"
		} else if d.IsRemovable {
			kind = "removable"
		}
		media := "SSD"
		if d.IsRotational {
			media = "HDD"
		}
		label := d.Name
		if d.Model != "" {
			label = label + "  " + truncate(d.Model, 22)
		}
		sb.WriteString(fmt.Sprintf(
			" [▸](fg:green,modifier:bold) [%s](fg:green,modifier:bold)  [%s/%s](fg:green)\n",
			label, kind, media,
		))

		if len(d.Partitions) == 0 {
			status := d.Status
			if status == "" {
				status = "unavailable"
			}
			sb.WriteString(fmt.Sprintf("   [status: %s](fg:yellow)\n", status))
			continue
		}

		for _, p := range d.Partitions {
			if !p.HasUsage {
				sb.WriteString(fmt.Sprintf(
					"   [%-10s](fg:green) [locked / no mount](fg:yellow)\n",
					truncate(p.Name, 10),
				))
				continue
			}
			color := colorForPct(p.UsedPercent)
			// Suffix layout: " 99.9%  100 GiB / 100 GiB"
			suffix := fmt.Sprintf(" %5.1f%%  %s / %s",
				p.UsedPercent, formatBytes(p.UsedBytes), formatBytes(p.TotalBytes))
			// 3 leading spaces + 10-wide name + 1 space + bar + suffix.
			barW := innerW - 3 - 10 - 1 - len(suffix) - 2
			if barW < 6 {
				barW = 6
			}
			sb.WriteString(fmt.Sprintf(
				"   [%-10s](fg:green) [%s](%s)[%s](fg:green)\n",
				truncate(p.Name, 10), loadBar(p.UsedPercent, barW), color, suffix,
			))
		}
	}
	return sb.String()
}

// formatBytes returns a human-readable size string (KiB/MiB/GiB/TiB).
func formatBytes(b uint64) string {
	const (
		KiB = 1024.0
		MiB = 1024.0 * KiB
		GiB = 1024.0 * MiB
		TiB = 1024.0 * GiB
	)
	v := float64(b)
	switch {
	case v >= TiB:
		return fmt.Sprintf("%.1f TiB", v/TiB)
	case v >= GiB:
		return fmt.Sprintf("%.1f GiB", v/GiB)
	case v >= MiB:
		return fmt.Sprintf("%.0f MiB", v/MiB)
	case v >= KiB:
		return fmt.Sprintf("%.0f KiB", v/KiB)
	}
	return fmt.Sprintf("%d B", b)
}
