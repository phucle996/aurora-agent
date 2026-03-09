package agent

import (
	"aurora-agent/internal/adminrpc"
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
)

func (a *Agent) run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return a.runHealthLoop(gctx)
	})
	g.Go(func() error {
		return a.runProbeListener(gctx)
	})
	g.Go(func() error {
		return a.runAdminHeartbeatLoop(gctx)
	})
	g.Go(func() error {
		return a.runBasicMetricsLoop(gctx)
	})

	if err := g.Wait(); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func (a *Agent) runHealthLoop(ctx context.Context) error {
	t := time.NewTicker(a.cfg.HealthInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			_ = a.logHealth("ok")
		}
	}
}

func (a *Agent) logHealth(status string) error {
	a.logger.Log(context.Background(), slog.LevelDebug, "agent health", "status", status, "snapshot", a.health.Snapshot())
	return nil
}

func (a *Agent) shutdown(ctx context.Context) {
	if a.heartbeatClient != nil {
		if err := a.heartbeatClient.Close(); err != nil {
			a.logger.Warn("admin heartbeat client close failed", "error", err)
		}
	}
	a.health.SetAdminConnected(false)
	_ = ctx
}

func (a *Agent) runAdminHeartbeatLoop(ctx context.Context) error {
	if a.heartbeatClient == nil {
		return nil
	}

	send := func() {
		payload := adminrpc.HeartbeatPayload{
			AgentID:           a.cfg.NodeID,
			Hostname:          a.cfg.Hostname,
			AgentVersion:      a.cfg.AgentVersion,
			AgentProbeAddr:    a.cfg.ProbeListenAddr,
			AgentGRPCEndpoint: a.cfg.AgentGRPCEndpoint,
			Platform:          a.cfg.Platform,
		}
		if err := a.heartbeatClient.ReportHeartbeat(ctx, payload); err != nil {
			a.logger.Warn("report heartbeat to admin failed", "error", err)
			a.health.SetAdminConnected(false)
			return
		}
		a.health.SetAdminConnected(true)
		a.health.MarkHeartbeat(time.Now().UTC())
		a.logger.Debug("reported heartbeat to admin", "agent_id", a.cfg.NodeID)
	}

	send()
	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			send()
		}
	}
}
