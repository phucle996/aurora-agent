package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type MemoryInfo struct {
	TotalBytes uint64
	UsedBytes  uint64
	FreeBytes  uint64
}

func ReadMemoryInfo() (MemoryInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer f.Close()

	vals := map[string]uint64{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		v, convErr := strconv.ParseUint(parts[1], 10, 64)
		if convErr != nil {
			continue
		}
		vals[key] = v * 1024
	}
	if err := s.Err(); err != nil {
		return MemoryInfo{}, fmt.Errorf("scan /proc/meminfo: %w", err)
	}
	total := vals["MemTotal"]
	avail := vals["MemAvailable"]
	if total == 0 {
		return MemoryInfo{}, fmt.Errorf("MemTotal missing")
	}
	used := total - avail
	return MemoryInfo{TotalBytes: total, UsedBytes: used, FreeBytes: avail}, nil
}
