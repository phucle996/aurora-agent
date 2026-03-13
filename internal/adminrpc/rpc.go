package adminrpc

import (
	"aurora-agent/internal/model"
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
)

func (c *HeartbeatClient) ReportHeartbeat(ctx context.Context, payload HeartbeatPayload) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("heartbeat client is nil")
	}

	req, err := structpb.NewStruct(map[string]any{
		"agent_id":            strings.TrimSpace(payload.AgentID),
		"hostname":            strings.TrimSpace(payload.Hostname),
		"agent_version":       strings.TrimSpace(payload.AgentVersion),
		"agent_probe_addr":    strings.TrimSpace(payload.AgentProbeAddr),
		"agent_grpc_endpoint": strings.TrimSpace(payload.AgentGRPCEndpoint),
		"platform":            strings.TrimSpace(payload.Platform),
	})
	if err != nil {
		return fmt.Errorf("build heartbeat request failed: %w", err)
	}

	callCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, defaultInvokeTimeout)
		defer cancel()
	}

	resp := &structpb.Struct{}
	if err := c.invokeWithRecovery(callCtx, runtimeReportAgentHeartbeatPath, req, resp); err != nil {
		return fmt.Errorf("report heartbeat failed: %w", err)
	}
	if err := c.maybeSyncHostRouting(callCtx, strings.TrimSpace(payload.AgentID)); err != nil && c.logger != nil {
		c.logger.Warn("sync host routing snapshot failed", "error", err)
	}
	if c.logger != nil {
		c.logger.Debug("agent heartbeat acknowledged by admin")
	}
	return nil
}

func (c *HeartbeatClient) GetMetricsPolicy(ctx context.Context, agentID string) (MetricsPolicy, error) {
	if c == nil || c.conn == nil {
		return MetricsPolicy{}, fmt.Errorf("heartbeat client is nil")
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultInvokeTimeout)
	defer cancel()
	req, err := structpb.NewStruct(map[string]any{
		"agent_id": strings.TrimSpace(agentID),
	})
	if err != nil {
		return MetricsPolicy{}, err
	}
	resp := &structpb.Struct{}
	if err := c.invokeWithRecovery(callCtx, runtimeGetAgentMetricsPolicyPath, req, resp); err != nil {
		return MetricsPolicy{}, err
	}

	defaultPolicy := MetricsPolicy{
		StreamEnabled:        false,
		BatchFlushInterval:   3 * time.Minute,
		BatchSampleInterval:  10 * time.Second,
		StreamSampleInterval: 3 * time.Second,
		MaxBatchRecords:      2048,
	}
	p := defaultPolicy
	p.StreamEnabled = readStructBool(resp, "stream_enabled", defaultPolicy.StreamEnabled)
	p.BatchFlushInterval = readStructSecondsAsDuration(resp, "batch_flush_interval_seconds", defaultPolicy.BatchFlushInterval)
	p.BatchSampleInterval = readStructSecondsAsDuration(resp, "batch_sample_interval_seconds", defaultPolicy.BatchSampleInterval)
	p.StreamSampleInterval = readStructSecondsAsDuration(resp, "stream_sample_interval_seconds", defaultPolicy.StreamSampleInterval)
	p.MaxBatchRecords = int(readStructNumber(resp, "max_batch_records", float64(defaultPolicy.MaxBatchRecords)))
	if p.MaxBatchRecords <= 0 {
		p.MaxBatchRecords = defaultPolicy.MaxBatchRecords
	}
	return p, nil
}

func (c *HeartbeatClient) ReportMetrics(
	ctx context.Context,
	agentID string,
	mode string,
	records []model.AgentBasicMetricRecord,
) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	if len(records) == 0 {
		return nil
	}

	list := make([]any, 0, len(records))
	for _, record := range records {
		services := make([]any, 0, len(record.Services))
		for _, serviceRecord := range record.Services {
			services = append(services, map[string]any{
				"service":               serviceRecord.Service,
				"cpu_usage_percent":     serviceRecord.CPUUsagePercent,
				"memory_used_bytes":     serviceRecord.MemoryUsedBytes,
				"disk_read_bps":         serviceRecord.DiskReadBps,
				"disk_write_bps":        serviceRecord.DiskWriteBps,
				"network_rx_bps":        serviceRecord.NetworkRxBps,
				"network_tx_bps":        serviceRecord.NetworkTxBps,
				"gpu_util_percent":      serviceRecord.GPUUtilPercent,
				"gpu_memory_used_bytes": serviceRecord.GPUMemoryUsedBytes,
			})
		}

		list = append(list, map[string]any{
			"ts_ms":              record.TimestampUnixMillis,
			"cpu_usage_percent":  record.CPUUsagePercent,
			"memory_used_bytes":  record.MemoryUsedBytes,
			"memory_total_bytes": record.MemoryTotalBytes,
			"disk_read_bps":      record.DiskReadBps,
			"disk_write_bps":     record.DiskWriteBps,
			"network_rx_bps":     record.NetworkRxBps,
			"network_tx_bps":     record.NetworkTxBps,
			"gpu": map[string]any{
				"count":              record.GPU.Count,
				"util_percent":       record.GPU.UtilPercent,
				"memory_used_bytes":  record.GPU.MemoryUsedBytes,
				"memory_total_bytes": record.GPU.MemoryTotalBytes,
			},
			"services":       services,
			"uptime_seconds": record.UptimeSeconds,
		})
	}

	req, err := structpb.NewStruct(map[string]any{
		"agent_id": strings.TrimSpace(agentID),
		"mode":     strings.TrimSpace(strings.ToLower(mode)),
		"records":  list,
	})
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultInvokeTimeout)
	defer cancel()
	resp := &structpb.Struct{}
	if err := c.invokeWithRecovery(callCtx, runtimeReportAgentMetricsPath, req, resp); err != nil {
		return err
	}
	return nil
}

func (c *HeartbeatClient) invokeWithRecovery(
	ctx context.Context,
	method string,
	req *structpb.Struct,
	resp *structpb.Struct,
) error {
	if c == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	if renewErr := c.maybeRenewClientCertificate(ctx); renewErr != nil && c.logger != nil {
		c.logger.Warn("preflight certificate renewal check failed", "error", renewErr)
	}
	conn := c.currentConn()
	if conn == nil {
		if err := c.reconnect(); err != nil {
			return classifyAdminRPCError(err, c.cfg.AdminServerCAPath)
		}
		conn = c.currentConn()
		if conn == nil {
			return fmt.Errorf("admin rpc connection is unavailable")
		}
	}

	err := conn.Invoke(ctx, method, req, resp)
	if err == nil {
		return nil
	}

	classified := classifyAdminRPCError(err, c.cfg.AdminServerCAPath)
	if !isRecoverableAdminRPCError(err) {
		return classified
	}

	if reconnectErr := c.reconnect(); reconnectErr != nil {
		return fmt.Errorf("%w; reconnect failed: %v", classified, classifyAdminRPCError(reconnectErr, c.cfg.AdminServerCAPath))
	}
	retryConn := c.currentConn()
	if retryConn == nil {
		return fmt.Errorf("%w; reconnect failed: connection unavailable", classified)
	}
	retryErr := retryConn.Invoke(ctx, method, req, resp)
	if retryErr == nil {
		return nil
	}
	retryClassified := classifyAdminRPCError(retryErr, c.cfg.AdminServerCAPath)

	if shouldTryBootstrapRotation(retryErr, c.cfg.BootstrapToken) {
		if c.logger != nil {
			c.logger.Warn("admin rpc failed; trying agent cert rotation via bootstrap token")
		}
		if rotateErr := ensureAgentClientCertificate(c.cfg, c.target, c.inferredServerName, c.logger, true); rotateErr == nil {
			if reconnectErr := c.reconnect(); reconnectErr == nil {
				lastConn := c.currentConn()
				if lastConn != nil {
					lastErr := lastConn.Invoke(ctx, method, req, resp)
					if lastErr == nil {
						return nil
					}
					return classifyAdminRPCError(lastErr, c.cfg.AdminServerCAPath)
				}
			}
		} else if c.logger != nil {
			c.logger.Warn("agent cert rotation via bootstrap token failed", "error", rotateErr)
		}
	}

	return retryClassified
}
