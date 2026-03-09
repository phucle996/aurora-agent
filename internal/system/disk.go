package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type DiskCounters struct {
	ReadBytes  uint64
	WriteBytes uint64
}

func ReadDiskCounters() (DiskCounters, error) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return DiskCounters{}, fmt.Errorf("open /proc/diskstats: %w", err)
	}
	defer f.Close()

	var out DiskCounters
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 14 {
			continue
		}
		dev := parts[2]
		if !isBlockDevice(dev) {
			continue
		}
		sectorsRead, errRead := strconv.ParseUint(parts[5], 10, 64)
		sectorsWritten, errWrite := strconv.ParseUint(parts[9], 10, 64)
		if errRead != nil || errWrite != nil {
			continue
		}
		out.ReadBytes += sectorsRead * 512
		out.WriteBytes += sectorsWritten * 512
	}
	if err := s.Err(); err != nil {
		return DiskCounters{}, fmt.Errorf("scan /proc/diskstats: %w", err)
	}
	return out, nil
}

func isBlockDevice(name string) bool {
	if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "fd") {
		return false
	}
	if strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "vd") || strings.HasPrefix(name, "xvd") {
		return true
	}
	return false
}
