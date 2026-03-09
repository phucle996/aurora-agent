package agent

import (
	"sync/atomic"
	"time"
)

type HealthStatus struct {
	adminConnected  atomic.Bool
	lastMetricAt    atomic.Int64
	lastHeartbeatAt atomic.Int64
}

func NewHealthStatus() *HealthStatus {
	h := &HealthStatus{}
	h.adminConnected.Store(false)
	return h
}

func (h *HealthStatus) SetAdminConnected(ok bool) {
	h.adminConnected.Store(ok)
}

func (h *HealthStatus) MarkNodeSample(ts time.Time) {
	h.lastMetricAt.Store(ts.UnixNano())
}

func (h *HealthStatus) MarkHeartbeat(ts time.Time) {
	h.lastHeartbeatAt.Store(ts.UnixNano())
}

func (h *HealthStatus) Snapshot() map[string]any {
	out := map[string]any{
		"admin_connected": h.adminConnected.Load(),
	}
	if v := h.lastMetricAt.Load(); v > 0 {
		out["last_metric_sample_at"] = time.Unix(0, v).UTC()
	}
	if v := h.lastHeartbeatAt.Load(); v > 0 {
		out["last_heartbeat_at"] = time.Unix(0, v).UTC()
	}
	return out
}
