package model

type AgentBasicMetricRecord struct {
	TimestampUnixMillis int64                      `json:"ts_ms"`
	CPUUsagePercent     float64                    `json:"cpu_usage_percent"`
	MemoryUsedBytes     uint64                     `json:"memory_used_bytes"`
	MemoryTotalBytes    uint64                     `json:"memory_total_bytes"`
	DiskReadBps         float64                    `json:"disk_read_bps"`
	DiskWriteBps        float64                    `json:"disk_write_bps"`
	NetworkRxBps        float64                    `json:"network_rx_bps"`
	NetworkTxBps        float64                    `json:"network_tx_bps"`
	GPU                 AgentGPUMetricRecord       `json:"gpu"`
	Services            []AgentServiceMetricRecord `json:"services"`
	UptimeSeconds       uint64                     `json:"uptime_seconds"`
}

type AgentGPUMetricRecord struct {
	Count            uint64  `json:"count"`
	UtilPercent      float64 `json:"util_percent"`
	MemoryUsedBytes  uint64  `json:"memory_used_bytes"`
	MemoryTotalBytes uint64  `json:"memory_total_bytes"`
}

type AgentServiceMetricRecord struct {
	Service            string  `json:"service"`
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`
	MemoryUsedBytes    uint64  `json:"memory_used_bytes"`
	DiskReadBps        float64 `json:"disk_read_bps"`
	DiskWriteBps       float64 `json:"disk_write_bps"`
	NetworkRxBps       float64 `json:"network_rx_bps"`
	NetworkTxBps       float64 `json:"network_tx_bps"`
	GPUUtilPercent     float64 `json:"gpu_util_percent"`
	GPUMemoryUsedBytes uint64  `json:"gpu_memory_used_bytes"`
}
