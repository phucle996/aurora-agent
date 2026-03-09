package model

type AgentBasicMetricRecord struct {
	TimestampUnixMillis int64   `json:"ts_ms"`
	CPUUsagePercent     float64 `json:"cpu_usage_percent"`
	MemoryUsedPercent   float64 `json:"memory_used_percent"`
	DiskReadBps         float64 `json:"disk_read_bps"`
	DiskWriteBps        float64 `json:"disk_write_bps"`
	NetworkRxBps        float64 `json:"network_rx_bps"`
	NetworkTxBps        float64 `json:"network_tx_bps"`
	UptimeSeconds       uint64  `json:"uptime_seconds"`
}
