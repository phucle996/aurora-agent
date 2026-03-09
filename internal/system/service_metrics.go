package system

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const defaultSystemctlTimeout = 5 * time.Second

type ServiceUnitMetrics struct {
	Name               string
	ActiveState        string
	CPUUsageNSec       uint64
	MemoryCurrentBytes uint64
	IOReadBytes        uint64
	IOWriteBytes       uint64
	IPIngressBytes     uint64
	IPEgressBytes      uint64
	ControlGroup       string
}

func ListAuroraServiceUnits(ctx context.Context) ([]string, error) {
	out, err := runCommandWithTimeout(ctx, defaultSystemctlTimeout, "systemctl", "list-unit-files", "--type=service", "--no-legend", "--no-pager", "aurora-*.service")
	if err != nil {
		return nil, err
	}

	units := make([]string, 0, 8)
	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		unit := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(unit, "aurora-") || !strings.HasSuffix(unit, ".service") {
			continue
		}
		if _, ok := seen[unit]; ok {
			continue
		}
		seen[unit] = struct{}{}
		units = append(units, unit)
	}
	sort.Strings(units)
	return units, nil
}

func ReadServiceUnitMetrics(ctx context.Context, unit string) (ServiceUnitMetrics, error) {
	name := strings.TrimSpace(unit)
	if name == "" {
		return ServiceUnitMetrics{}, fmt.Errorf("service unit is empty")
	}

	out, err := runCommandWithTimeout(ctx, defaultSystemctlTimeout, "systemctl", "show", name, "--no-pager",
		"--property=Id",
		"--property=ActiveState",
		"--property=CPUUsageNSec",
		"--property=MemoryCurrent",
		"--property=IOReadBytes",
		"--property=IOWriteBytes",
		"--property=IPIngressBytes",
		"--property=IPEgressBytes",
		"--property=ControlGroup",
	)
	if err != nil {
		return ServiceUnitMetrics{}, err
	}

	fields := make(map[string]string, 10)
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		fields[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	record := ServiceUnitMetrics{
		Name:               readStringField(fields, "Id", name),
		ActiveState:        readStringField(fields, "ActiveState", ""),
		CPUUsageNSec:       readUintField(fields, "CPUUsageNSec"),
		MemoryCurrentBytes: readUintField(fields, "MemoryCurrent"),
		IOReadBytes:        readUintField(fields, "IOReadBytes"),
		IOWriteBytes:       readUintField(fields, "IOWriteBytes"),
		IPIngressBytes:     readUintField(fields, "IPIngressBytes"),
		IPEgressBytes:      readUintField(fields, "IPEgressBytes"),
		ControlGroup:       readStringField(fields, "ControlGroup", ""),
	}
	return record, nil
}

func ReadServiceCgroupPIDs(controlGroup string) ([]int, error) {
	group := strings.TrimSpace(controlGroup)
	if group == "" || group == "/" {
		return nil, nil
	}

	group = strings.TrimPrefix(group, "/")
	path := filepath.Join("/sys/fs/cgroup", group, "cgroup.procs")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}

	seen := make(map[int]struct{})
	out := make([]int, 0, 8)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		pid, convErr := strconv.Atoi(line)
		if convErr != nil || pid <= 0 {
			continue
		}
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		out = append(out, pid)
	}
	sort.Ints(out)
	return out, nil
}

func runCommandWithTimeout(parent context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = defaultSystemctlTimeout
	}
	ctx := parent
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(callCtx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	return string(output), nil
}

func readUintField(fields map[string]string, key string) uint64 {
	if len(fields) == 0 {
		return 0
	}
	raw := strings.TrimSpace(fields[key])
	if raw == "" || raw == "infinity" || raw == "[not set]" {
		return 0
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func readStringField(fields map[string]string, key, fallback string) string {
	if len(fields) == 0 {
		return fallback
	}
	v := strings.TrimSpace(fields[key])
	if v == "" {
		return fallback
	}
	return v
}
