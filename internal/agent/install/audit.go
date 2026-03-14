package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type auditEvent struct {
	Timestamp        string `json:"timestamp"`
	Action           string `json:"action"`
	RequestID        string `json:"request_id,omitempty"`
	Module           string `json:"module"`
	Version          string `json:"version,omitempty"`
	Runtime          string `json:"runtime,omitempty"`
	ArtifactURL      string `json:"artifact_url,omitempty"`
	ArtifactChecksum string `json:"artifact_checksum,omitempty"`
	Status           string `json:"status"`
	OK               bool   `json:"ok"`
	ErrorText        string `json:"error_text,omitempty"`
}

func appendAuditEvent(event auditEvent) error {
	policy := currentPolicy()
	path := strings.TrimSpace(policy.AuditLogPath)
	if path == "" {
		return nil
	}
	event.Timestamp = strings.TrimSpace(event.Timestamp)
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode install audit event failed: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create install audit dir failed: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open install audit log failed: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write install audit event failed: %w", err)
	}
	return nil
}
