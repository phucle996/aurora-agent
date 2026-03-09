package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type NetCounters struct {
	RxBytes uint64
	TxBytes uint64
}

func ReadNetCounters() (NetCounters, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return NetCounters{}, fmt.Errorf("open /proc/net/dev: %w", err)
	}
	defer f.Close()

	var out NetCounters
	s := bufio.NewScanner(f)
	lineNo := 0
	for s.Scan() {
		lineNo++
		if lineNo <= 2 {
			continue
		}
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" || iface == "" {
			continue
		}
		metrics := strings.Fields(strings.TrimSpace(parts[1]))
		if len(metrics) < 16 {
			continue
		}
		rx, rxErr := strconv.ParseUint(metrics[0], 10, 64)
		tx, txErr := strconv.ParseUint(metrics[8], 10, 64)
		if rxErr != nil || txErr != nil {
			continue
		}
		out.RxBytes += rx
		out.TxBytes += tx
	}
	if err := s.Err(); err != nil {
		return NetCounters{}, fmt.Errorf("scan /proc/net/dev: %w", err)
	}
	return out, nil
}
