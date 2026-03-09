package main

import (
	"context"
	"log"

	"aurora-agent/internal/agent"
	"aurora-agent/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger := agent.BuildLogger(cfg)
	a, err := agent.New(cfg, logger)
	if err != nil {
		logger.Error("agent initialization failed", "error", err)
		return
	}

	if err := a.Run(context.Background()); err != nil {
		logger.Error("agent runtime failed", "error", err)
	}
}
