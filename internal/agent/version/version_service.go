package version

import (
	"time"

	"aurora-agent/internal/config"
)

func Get(cfg config.Config, _ *GetVersionRequest) *GetVersionResponse {
	return &GetVersionResponse{
		NodeID:          cfg.NodeID,
		AgentVersion:    cfg.AgentVersion,
		ProbeListenAddr: cfg.ProbeListenAddr,
		CheckedAtUnix:   time.Now().UTC().Unix(),
	}
}
