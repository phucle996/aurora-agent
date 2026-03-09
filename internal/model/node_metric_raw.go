package model

// NodeMetricsRaw is a raw node counter snapshot sent to backend without local delta/rate calculations.
type NodeMetricsRaw struct {
	NodeID        string                `json:"node_id"`
	TimestampUnix int64                 `json:"timestamp"`
	CPU           NodeCPUCounterRaw     `json:"cpu"`
	Memory        NodeMemoryCounterRaw  `json:"memory"`
	Disk          NodeDiskCounterRaw    `json:"disk"`
	Network       NodeNetworkCounterRaw `json:"network"`
	Load          NodeLoadRaw           `json:"load"`
	UptimeSeconds uint64                `json:"uptime_seconds"`
}

type NodeCPUCounterRaw struct {
	Total  uint64 `json:"total"`
	User   uint64 `json:"user"`
	System uint64 `json:"system"`
	Idle   uint64 `json:"idle"`
	IOWait uint64 `json:"iowait"`
}

type NodeMemoryCounterRaw struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
}

type NodeDiskCounterRaw struct {
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	ReadIOs    uint64 `json:"read_ios"`
	WriteIOs   uint64 `json:"write_ios"`
	IOTimeMs   uint64 `json:"io_time_ms"`
}

type NodeNetworkCounterRaw struct {
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
}

type NodeLoadRaw struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}
