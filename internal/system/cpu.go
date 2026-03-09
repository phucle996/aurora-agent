package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type CPUCounters struct {
	User    uint64
	Nice    uint64
	System  uint64
	Idle    uint64
	IOWait  uint64
	IRQ     uint64
	SoftIRQ uint64
	Steal   uint64
	Total   uint64
}

func ReadCPUCounters() (CPUCounters, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUCounters{}, fmt.Errorf("open /proc/stat: %w", err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 8 {
			return CPUCounters{}, fmt.Errorf("unexpected cpu line: %q", line)
		}
		vals := make([]uint64, 0, len(parts)-1)
		for _, p := range parts[1:] {
			v, convErr := strconv.ParseUint(p, 10, 64)
			if convErr != nil {
				return CPUCounters{}, fmt.Errorf("parse cpu stat %q: %w", p, convErr)
			}
			vals = append(vals, v)
		}
		c := CPUCounters{}
		if len(vals) > 0 {
			c.User = vals[0]
		}
		if len(vals) > 1 {
			c.Nice = vals[1]
		}
		if len(vals) > 2 {
			c.System = vals[2]
		}
		if len(vals) > 3 {
			c.Idle = vals[3]
		}
		if len(vals) > 4 {
			c.IOWait = vals[4]
		}
		if len(vals) > 5 {
			c.IRQ = vals[5]
		}
		if len(vals) > 6 {
			c.SoftIRQ = vals[6]
		}
		if len(vals) > 7 {
			c.Steal = vals[7]
		}
		for _, v := range vals {
			c.Total += v
		}
		return c, nil
	}
	if err := s.Err(); err != nil {
		return CPUCounters{}, fmt.Errorf("scan /proc/stat: %w", err)
	}
	return CPUCounters{}, fmt.Errorf("cpu aggregate line not found")
}

func CPUUsage(prev, cur CPUCounters) float64 {
	if cur.Total <= prev.Total {
		return 0
	}
	totalDelta := float64(cur.Total - prev.Total)
	idledPrev := prev.Idle + prev.IOWait
	idledCur := cur.Idle + cur.IOWait
	idleDelta := float64(idledCur - idledPrev)
	if idleDelta < 0 {
		idleDelta = 0
	}
	usage := ((totalDelta - idleDelta) / totalDelta) * 100
	if usage < 0 {
		return 0
	}
	if usage > 100 {
		return 100
	}
	return usage
}
