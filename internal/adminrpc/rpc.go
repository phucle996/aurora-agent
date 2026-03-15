package adminrpc

import (
	"aurora-agent/internal/model"
	runtimev1 "github.com/phucle996/aurora-proto/runtimev1"
	"context"
	"fmt"
	"strings"
	"time"
)

func (c *HeartbeatClient) ReportHeartbeat(ctx context.Context, payload HeartbeatPayload) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("heartbeat client is nil")
	}

	req := &runtimev1.ReportAgentHeartbeatRequest{
		AgentId:           strings.TrimSpace(payload.AgentID),
		Hostname:          strings.TrimSpace(payload.Hostname),
		AgentVersion:      strings.TrimSpace(payload.AgentVersion),
		AgentProbeAddr:    strings.TrimSpace(payload.AgentProbeAddr),
		AgentGrpcEndpoint: strings.TrimSpace(payload.AgentGRPCEndpoint),
		Platform:          strings.TrimSpace(payload.Platform),
		Architecture:      strings.TrimSpace(payload.Architecture),
	}

	callCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, defaultInvokeTimeout)
		defer cancel()
	}

	if err := c.invokeWithRecovery(callCtx, func(client runtimev1.RuntimeServiceClient) error {
		_, err := client.ReportAgentHeartbeat(callCtx, req)
		return err
	}); err != nil {
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
	req := &runtimev1.GetAgentMetricsPolicyRequest{AgentId: strings.TrimSpace(agentID)}
	var resp *runtimev1.GetAgentMetricsPolicyResponse
	if err := c.invokeWithRecovery(callCtx, func(client runtimev1.RuntimeServiceClient) error {
		var callErr error
		resp, callErr = client.GetAgentMetricsPolicy(callCtx, req)
		return callErr
	}); err != nil {
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
	if resp == nil {
		return p, nil
	}
	p.StreamEnabled = resp.GetStreamEnabled()
	p.BatchFlushInterval = secondsAsDuration(resp.GetBatchFlushIntervalSeconds(), defaultPolicy.BatchFlushInterval)
	p.BatchSampleInterval = secondsAsDuration(resp.GetBatchSampleIntervalSeconds(), defaultPolicy.BatchSampleInterval)
	p.StreamSampleInterval = secondsAsDuration(resp.GetStreamSampleIntervalSeconds(), defaultPolicy.StreamSampleInterval)
	if max := int(resp.GetMaxBatchRecords()); max > 0 {
		p.MaxBatchRecords = max
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

	items := make([]*runtimev1.AgentMetricRecord, 0, len(records))
	for _, record := range records {
		services := make([]*runtimev1.AgentServiceMetricRecord, 0, len(record.Services))
		for _, serviceRecord := range record.Services {
			services = append(services, &runtimev1.AgentServiceMetricRecord{
				Service:            serviceRecord.Service,
				CpuUsagePercent:    serviceRecord.CPUUsagePercent,
				MemoryUsedBytes:    serviceRecord.MemoryUsedBytes,
				DiskReadBps:        serviceRecord.DiskReadBps,
				DiskWriteBps:       serviceRecord.DiskWriteBps,
				NetworkRxBps:       serviceRecord.NetworkRxBps,
				NetworkTxBps:       serviceRecord.NetworkTxBps,
				GpuUtilPercent:     serviceRecord.GPUUtilPercent,
				GpuMemoryUsedBytes: serviceRecord.GPUMemoryUsedBytes,
			})
		}
		items = append(items, &runtimev1.AgentMetricRecord{
			TsMs:             record.TimestampUnixMillis,
			CpuUsagePercent:  record.CPUUsagePercent,
			MemoryUsedBytes:  record.MemoryUsedBytes,
			MemoryTotalBytes: record.MemoryTotalBytes,
			DiskReadBps:      record.DiskReadBps,
			DiskWriteBps:     record.DiskWriteBps,
			NetworkRxBps:     record.NetworkRxBps,
			NetworkTxBps:     record.NetworkTxBps,
			Gpu: &runtimev1.AgentGPUMetricRecord{
				Count:            record.GPU.Count,
				UtilPercent:      record.GPU.UtilPercent,
				MemoryUsedBytes:  record.GPU.MemoryUsedBytes,
				MemoryTotalBytes: record.GPU.MemoryTotalBytes,
			},
			Services:      services,
			UptimeSeconds: record.UptimeSeconds,
		})
	}

	req := &runtimev1.ReportAgentMetricsRequest{
		AgentId: strings.TrimSpace(agentID),
		Mode:    strings.TrimSpace(strings.ToLower(mode)),
		Records: items,
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultInvokeTimeout)
	defer cancel()
	return c.invokeWithRecovery(callCtx, func(client runtimev1.RuntimeServiceClient) error {
		_, err := client.ReportAgentMetrics(callCtx, req)
		return err
	})
}

func (c *HeartbeatClient) invokeWithRecovery(
	ctx context.Context,
	invoke func(runtimev1.RuntimeServiceClient) error,
) error {
	if c == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	if invoke == nil {
		return fmt.Errorf("runtime invoke callback is nil")
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

	err := invoke(c.runtimeClient(conn))
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
	retryErr := invoke(c.runtimeClient(retryConn))
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
					lastErr := invoke(c.runtimeClient(lastConn))
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

func secondsAsDuration(seconds int64, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
