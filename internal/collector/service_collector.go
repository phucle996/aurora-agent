package collector

import (
	"aurora-agent/internal/model"
	"aurora-agent/internal/system"
	"context"
	"sort"
	"strings"
	"time"
)

type ServiceCountersSnapshot struct {
	Service            string
	TimestampUnixMS    int64
	CPUUsageNSec       uint64
	MemoryUsedBytes    uint64
	DiskReadBytes      uint64
	DiskWriteBytes     uint64
	NetworkRxBytes     uint64
	NetworkTxBytes     uint64
	GPUUtilPercent     float64
	GPUMemoryUsedBytes uint64
}

type ServiceCollector struct{}

func NewServiceCollector() *ServiceCollector {
	return &ServiceCollector{}
}

func (c *ServiceCollector) Collect(ctx context.Context) ([]ServiceCountersSnapshot, model.AgentGPUMetricRecord, error) {
	_ = c
	hostGPU, _ := system.ReadGPUSnapshot(ctx)
	processGPU, _ := system.ReadGPUProcessUsage(ctx)

	units, err := system.ListAuroraServiceUnits(ctx)
	if err != nil {
		return []ServiceCountersSnapshot{}, toHostGPURecord(hostGPU), nil
	}
	out := make([]ServiceCountersSnapshot, 0, len(units))
	now := time.Now().UTC().UnixMilli()

	for _, unit := range units {
		metrics, readErr := system.ReadServiceUnitMetrics(ctx, unit)
		if readErr != nil {
			continue
		}
		if strings.TrimSpace(metrics.ActiveState) != "active" {
			continue
		}

		snapshot := ServiceCountersSnapshot{
			Service:         strings.TrimSpace(metrics.Name),
			TimestampUnixMS: now,
			CPUUsageNSec:    metrics.CPUUsageNSec,
			MemoryUsedBytes: metrics.MemoryCurrentBytes,
			DiskReadBytes:   metrics.IOReadBytes,
			DiskWriteBytes:  metrics.IOWriteBytes,
			NetworkRxBytes:  metrics.IPIngressBytes,
			NetworkTxBytes:  metrics.IPEgressBytes,
		}

		pids, _ := system.ReadServiceCgroupPIDs(metrics.ControlGroup)
		for _, pid := range pids {
			gpuUsage, ok := processGPU[pid]
			if !ok {
				continue
			}
			snapshot.GPUMemoryUsedBytes += gpuUsage.MemoryUsedBytes
			if gpuUsage.UtilPercent > snapshot.GPUUtilPercent {
				snapshot.GPUUtilPercent = gpuUsage.UtilPercent
			}
		}
		if snapshot.Service != "" {
			out = append(out, snapshot)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].Service < out[j].Service
	})

	return out, toHostGPURecord(hostGPU), nil
}

func toHostGPURecord(snapshot system.GPUSnapshot) model.AgentGPUMetricRecord {
	return model.AgentGPUMetricRecord{
		Count:            snapshot.Count,
		UtilPercent:      snapshot.UtilPercent,
		MemoryUsedBytes:  snapshot.MemoryUsedBytes,
		MemoryTotalBytes: snapshot.MemoryTotalBytes,
	}
}
