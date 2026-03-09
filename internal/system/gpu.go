package system

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const mibToBytes uint64 = 1024 * 1024

type GPUSnapshot struct {
	Count            uint64
	UtilPercent      float64
	MemoryUsedBytes  uint64
	MemoryTotalBytes uint64
}

type GPUProcessUsage struct {
	PID             int
	UtilPercent     float64
	MemoryUsedBytes uint64
}

func ReadGPUSnapshot(ctx context.Context) (GPUSnapshot, error) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return GPUSnapshot{}, nil
	}

	out, err := runCommandWithTimeout(ctx, 5*time.Second, "nvidia-smi", "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return GPUSnapshot{}, nil
	}

	var count uint64
	var utilSum float64
	var memoryUsed uint64
	var memoryTotal uint64

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}

		util := parseFloat(parts[0])
		usedMiB := parseUint(parts[1])
		totalMiB := parseUint(parts[2])

		utilSum += util
		memoryUsed += usedMiB * mibToBytes
		memoryTotal += totalMiB * mibToBytes
		count++
	}

	outSnapshot := GPUSnapshot{
		Count:            count,
		MemoryUsedBytes:  memoryUsed,
		MemoryTotalBytes: memoryTotal,
	}
	if count > 0 {
		outSnapshot.UtilPercent = utilSum / float64(count)
	}
	return outSnapshot, nil
}

func ReadGPUProcessUsage(ctx context.Context) (map[int]GPUProcessUsage, error) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return map[int]GPUProcessUsage{}, nil
	}

	out, err := runCommandWithTimeout(ctx, 5*time.Second, "nvidia-smi", "--query-compute-apps=pid,used_gpu_memory", "--format=csv,noheader,nounits")
	if err != nil {
		return map[int]GPUProcessUsage{}, nil
	}

	result := make(map[int]GPUProcessUsage)
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.Contains(strings.ToLower(line), "no running") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		pid := int(parseUint(parts[0]))
		if pid <= 0 {
			continue
		}
		usage := result[pid]
		usage.PID = pid
		usage.MemoryUsedBytes += parseUint(parts[1]) * mibToBytes
		result[pid] = usage
	}

	pmon, _ := runCommandWithTimeout(ctx, 5*time.Second, "nvidia-smi", "pmon", "-c", "1", "-s", "um")
	pmonScan := bufio.NewScanner(strings.NewReader(pmon))
	for pmonScan.Scan() {
		line := strings.TrimSpace(pmonScan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid64, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || pid64 <= 0 {
			continue
		}
		pid := int(pid64)
		usage := result[pid]
		usage.PID = pid
		smUtil := parseFloat(fields[3])
		if smUtil > usage.UtilPercent {
			usage.UtilPercent = smUtil
		}
		result[pid] = usage
	}

	return result, nil
}

func parseUint(raw string) uint64 {
	v := strings.TrimSpace(raw)
	if v == "" || v == "[not" || strings.Contains(strings.ToLower(v), "not supported") || v == "-" {
		return 0
	}
	num, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return num
}

func parseFloat(raw string) float64 {
	v := strings.TrimSpace(raw)
	if v == "" || strings.Contains(strings.ToLower(v), "not supported") || v == "-" {
		return 0
	}
	num, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return num
}
