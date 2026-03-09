package agent

import (
	"aurora-agent/internal/adminrpc"
	"aurora-agent/internal/collector"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aurora-agent/internal/config"
)

type Agent struct {
	cfg             config.Config
	logger          *slog.Logger
	metricsNode     *collector.NodeCollector
	metricsService  *collector.ServiceCollector
	health          *HealthStatus
	heartbeatClient *adminrpc.HeartbeatClient
}

func New(cfg config.Config, logger *slog.Logger) (*Agent, error) {
	heartbeatClient, err := adminrpc.NewHeartbeatClient(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("admin heartbeat client: %w", err)
	}

	nodeCollector := collector.NewNodeCollector(cfg.NodeID)
	serviceCollector := collector.NewServiceCollector()
	health := NewHealthStatus()

	return &Agent{
		cfg:             cfg,
		logger:          logger,
		metricsNode:     nodeCollector,
		metricsService:  serviceCollector,
		health:          health,
		heartbeatClient: heartbeatClient,
	}, nil
}

func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("starting aurora-agent", "node_id", a.cfg.NodeID, "probe_addr", a.cfg.ProbeListenAddr)
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- a.run(runCtx)
	}()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var runErr error
	select {
	case runErr = <-runErrCh:
		// Agent terminated by itself (startup error/runtime error/parent ctx canceled).
	case sig := <-sigCh:
		a.logger.Info("shutdown signal received, starting graceful shutdown", "signal", sig.String(), "timeout", a.cfg.ShutdownTimeout)
		cancelRun()

		graceTimer := time.NewTimer(a.cfg.ShutdownTimeout)
		defer graceTimer.Stop()

		select {
		case runErr = <-runErrCh:
			// graceful stop completed in time
		case sig2 := <-sigCh:
			a.logger.Warn("second signal received, forcing immediate shutdown", "signal", sig2.String())
			runErr = context.Canceled
		case <-graceTimer.C:
			a.logger.Warn("graceful shutdown timeout reached, forcing shutdown", "timeout", a.cfg.ShutdownTimeout)
			runErr = context.DeadlineExceeded
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancelShutdown()
	a.shutdown(shutdownCtx)

	if runErr != nil {
		return runErr
	}
	a.logger.Info("aurora-agent stopped")
	return nil
}

func BuildLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	hOpts := &slog.HandlerOptions{Level: level}
	return slog.New(slog.NewTextHandler(os.Stdout, hOpts))
}
