package agent

import (
	"aurora-agent/internal/adminrpc"
	"aurora-agent/internal/collector"
	"aurora-agent/internal/model"
	"context"
	"runtime"
	"time"
)

func (a *Agent) runBasicMetricsLoop(ctx context.Context) error {
	if a == nil || a.heartbeatClient == nil || a.metricsNode == nil {
		return nil
	}

	policy := adminrpc.MetricsPolicy{
		StreamEnabled:        false,
		BatchFlushInterval:   3 * time.Minute,
		BatchSampleInterval:  10 * time.Second,
		StreamSampleInterval: 3 * time.Second,
		MaxBatchRecords:      2048,
	}

	refreshPolicy := func() {
		loaded, err := a.heartbeatClient.GetMetricsPolicy(ctx, a.cfg.NodeID)
		if err != nil {
			a.logger.Warn("load metrics policy failed, keep current policy", "error", err)
			return
		}
		policy = loaded
	}

	refreshPolicy()

	policyTicker := time.NewTicker(15 * time.Second)
	defer policyTicker.Stop()

	sampleInterval := policy.BatchSampleInterval
	if policy.StreamEnabled {
		sampleInterval = policy.StreamSampleInterval
	}
	sampleTicker := time.NewTicker(sampleInterval)
	defer sampleTicker.Stop()

	flushTicker := time.NewTicker(policy.BatchFlushInterval)
	defer flushTicker.Stop()

	var prev model.NodeMetrics
	hasPrev := false
	prevServices := make(map[string]serviceCounterSnapshot)
	batch := make([]model.AgentBasicMetricRecord, 0, policy.MaxBatchRecords)

	resetTickers := func() {
		sampleTicker.Stop()
		flushTicker.Stop()

		nextSample := policy.BatchSampleInterval
		if policy.StreamEnabled {
			nextSample = policy.StreamSampleInterval
		}
		if nextSample <= 0 {
			nextSample = 10 * time.Second
		}
		nextFlush := policy.BatchFlushInterval
		if nextFlush <= 0 {
			nextFlush = 3 * time.Minute
		}
		sampleTicker = time.NewTicker(nextSample)
		flushTicker = time.NewTicker(nextFlush)
	}

	flushBatch := func() {
		if len(batch) == 0 {
			return
		}
		if err := a.heartbeatClient.ReportMetrics(ctx, a.cfg.NodeID, "batch", batch); err != nil {
			a.logger.Warn("report batch metrics failed", "error", err, "records", len(batch))
			return
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flushBatch()
			return nil
		case <-policyTicker.C:
			before := policy
			refreshPolicy()
			if before.StreamEnabled != policy.StreamEnabled ||
				before.BatchFlushInterval != policy.BatchFlushInterval ||
				before.BatchSampleInterval != policy.BatchSampleInterval ||
				before.StreamSampleInterval != policy.StreamSampleInterval {
				resetTickers()
			}
		case <-sampleTicker.C:
			raw, err := a.metricsNode.Collect(ctx)
			if err != nil {
				a.logger.Warn("collect basic metrics failed", "error", err)
				continue
			}

			hostGPU := model.AgentGPUMetricRecord{}
			serviceSnapshots := make([]collector.ServiceCountersSnapshot, 0, 8)
			if a.metricsService != nil {
				services, gpu, serviceErr := a.metricsService.Collect(ctx)
				if serviceErr != nil {
					a.logger.Warn("collect service metrics failed", "error", serviceErr)
				} else {
					hostGPU = gpu
					serviceSnapshots = services
				}
			}

			a.health.MarkNodeSample(time.Now().UTC())
			record := computeBasicMetricRecord(raw, prev, hasPrev)
			record.GPU = hostGPU
			record.Services, prevServices = computeServiceMetricRecords(serviceSnapshots, prevServices)

			prev = raw
			hasPrev = true

			if policy.StreamEnabled {
				if err := a.heartbeatClient.ReportMetrics(ctx, a.cfg.NodeID, "stream", []model.AgentBasicMetricRecord{record}); err != nil {
					a.logger.Warn("report stream metric failed", "error", err)
				}
				continue
			}

			batch = append(batch, record)
			if policy.MaxBatchRecords > 0 && len(batch) >= policy.MaxBatchRecords {
				flushBatch()
			}
		case <-flushTicker.C:
			if policy.StreamEnabled {
				continue
			}
			flushBatch()
		}
	}
}

type serviceCounterSnapshot struct {
	TimestampUnixMS int64
	CPUUsageNSec    uint64
	DiskReadBytes   uint64
	DiskWriteBytes  uint64
	NetworkRxBytes  uint64
	NetworkTxBytes  uint64
}

func computeBasicMetricRecord(cur model.NodeMetrics, prev model.NodeMetrics, hasPrev bool) model.AgentBasicMetricRecord {
	record := model.AgentBasicMetricRecord{
		TimestampUnixMillis: cur.TimestampUnix,
		MemoryUsedBytes:     cur.Memory.UsedBytes,
		MemoryTotalBytes:    cur.Memory.TotalBytes,
		UptimeSeconds:       cur.UptimeSeconds,
	}

	if !hasPrev || cur.TimestampUnix <= prev.TimestampUnix {
		return record
	}

	seconds := float64(cur.TimestampUnix-prev.TimestampUnix) / 1000.0
	if seconds <= 0 {
		return record
	}

	totalDelta := deltaCounterU64(cur.CPU.Total, prev.CPU.Total)
	if totalDelta > 0 {
		idleDelta := deltaCounterU64(cur.CPU.Idle, prev.CPU.Idle)
		busyDelta := totalDelta - idleDelta
		record.CPUUsagePercent = (float64(busyDelta) / float64(totalDelta)) * 100
	}

	record.DiskReadBps = float64(deltaCounterU64(cur.Disk.ReadBytes, prev.Disk.ReadBytes)) / seconds
	record.DiskWriteBps = float64(deltaCounterU64(cur.Disk.WriteBytes, prev.Disk.WriteBytes)) / seconds
	record.NetworkRxBps = float64(deltaCounterU64(cur.Network.RxBytes, prev.Network.RxBytes)) / seconds
	record.NetworkTxBps = float64(deltaCounterU64(cur.Network.TxBytes, prev.Network.TxBytes)) / seconds
	return record
}

func computeServiceMetricRecords(
	current []collector.ServiceCountersSnapshot,
	prev map[string]serviceCounterSnapshot,
) ([]model.AgentServiceMetricRecord, map[string]serviceCounterSnapshot) {
	if len(current) == 0 {
		return []model.AgentServiceMetricRecord{}, map[string]serviceCounterSnapshot{}
	}
	if prev == nil {
		prev = make(map[string]serviceCounterSnapshot)
	}

	cores := runtime.NumCPU()
	if cores <= 0 {
		cores = 1
	}

	out := make([]model.AgentServiceMetricRecord, 0, len(current))
	next := make(map[string]serviceCounterSnapshot, len(current))

	for _, item := range current {
		record := model.AgentServiceMetricRecord{
			Service:            item.Service,
			MemoryUsedBytes:    item.MemoryUsedBytes,
			GPUUtilPercent:     item.GPUUtilPercent,
			GPUMemoryUsedBytes: item.GPUMemoryUsedBytes,
		}

		if last, ok := prev[item.Service]; ok && item.TimestampUnixMS > last.TimestampUnixMS {
			elapsedSec := float64(item.TimestampUnixMS-last.TimestampUnixMS) / 1000.0
			elapsedNSec := (item.TimestampUnixMS - last.TimestampUnixMS) * int64(time.Millisecond)
			if elapsedSec > 0 {
				record.DiskReadBps = float64(deltaCounterU64(item.DiskReadBytes, last.DiskReadBytes)) / elapsedSec
				record.DiskWriteBps = float64(deltaCounterU64(item.DiskWriteBytes, last.DiskWriteBytes)) / elapsedSec
				record.NetworkRxBps = float64(deltaCounterU64(item.NetworkRxBytes, last.NetworkRxBytes)) / elapsedSec
				record.NetworkTxBps = float64(deltaCounterU64(item.NetworkTxBytes, last.NetworkTxBytes)) / elapsedSec
			}
			if elapsedNSec > 0 {
				cpuDelta := deltaCounterU64(item.CPUUsageNSec, last.CPUUsageNSec)
				record.CPUUsagePercent = clampPercent((float64(cpuDelta) / float64(elapsedNSec*int64(cores))) * 100)
			}
		}

		next[item.Service] = serviceCounterSnapshot{
			TimestampUnixMS: item.TimestampUnixMS,
			CPUUsageNSec:    item.CPUUsageNSec,
			DiskReadBytes:   item.DiskReadBytes,
			DiskWriteBytes:  item.DiskWriteBytes,
			NetworkRxBytes:  item.NetworkRxBytes,
			NetworkTxBytes:  item.NetworkTxBytes,
		}
		out = append(out, record)
	}

	return out, next
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func deltaCounterU64(cur, prev uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}
