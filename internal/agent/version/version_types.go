package version

type GetVersionRequest struct {
	NodeID string `json:"node_id"`
}

type GetVersionResponse struct {
	NodeID          string `json:"node_id"`
	AgentVersion    string `json:"agent_version"`
	ProbeListenAddr string `json:"probe_listen_addr"`
	CheckedAtUnix   int64  `json:"checked_at_unix"`
}
