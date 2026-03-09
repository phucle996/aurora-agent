package collector

import (
	"aurora-agent/internal/model"
	"aurora-agent/internal/system"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type NodeCollector struct {
	nodeID string
}

func NewNodeCollector(nodeID string) *NodeCollector {
	return &NodeCollector{nodeID: strings.TrimSpace(nodeID)}
}

func (c *NodeCollector) Collect(ctx context.Context) (model.NodeMetrics, error) {
	_ = ctx

	cpu, err := system.ReadCPUCounters()
	if err != nil {
		return model.NodeMetrics{}, fmt.Errorf("read cpu counters failed: %w", err)
	}
	mem, err := system.ReadMemoryInfo()
	if err != nil {
		return model.NodeMetrics{}, fmt.Errorf("read memory info failed: %w", err)
	}
	disk, err := system.ReadDiskCounters()
	if err != nil {
		return model.NodeMetrics{}, fmt.Errorf("read disk counters failed: %w", err)
	}
	netif, err := system.ReadNetCounters()
	if err != nil {
		return model.NodeMetrics{}, fmt.Errorf("read net counters failed: %w", err)
	}

	load1, load5, load15 := readLoadAverage()
	uptime := readUptimeSeconds()

	return model.NodeMetrics{
		NodeID:        c.nodeID,
		TimestampUnix: time.Now().UTC().UnixMilli(),
		CPU: model.NodeCPUCounterRaw{
			Total:  cpu.Total,
			User:   cpu.User,
			System: cpu.System,
			Idle:   cpu.Idle,
			IOWait: cpu.IOWait,
		},
		Memory: model.NodeMemoryCounterRaw{
			TotalBytes:     mem.TotalBytes,
			AvailableBytes: mem.FreeBytes,
			UsedBytes:      mem.UsedBytes,
		},
		Disk: model.NodeDiskCounterRaw{
			ReadBytes:  disk.ReadBytes,
			WriteBytes: disk.WriteBytes,
		},
		Network: model.NodeNetworkCounterRaw{
			RxBytes: netif.RxBytes,
			TxBytes: netif.TxBytes,
		},
		Load: model.NodeLoadRaw{
			Load1:  load1,
			Load5:  load5,
			Load15: load15,
		},
		UptimeSeconds: uptime,
	}, nil
}

func readLoadAverage() (float64, float64, float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	load1, _ := strconv.ParseFloat(fields[0], 64)
	load5, _ := strconv.ParseFloat(fields[1], 64)
	load15, _ := strconv.ParseFloat(fields[2], 64)
	return load1, load5, load15
}

func readUptimeSeconds() uint64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) == 0 {
		return 0
	}
	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || uptime < 0 {
		return 0
	}
	return uint64(uptime)
}
