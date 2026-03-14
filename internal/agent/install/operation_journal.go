package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var installedOperationsStatePath = "/var/lib/aurora-agent/install_operations.json"

const (
	operationActionInstall   = "install"
	operationActionRestart   = "restart"
	operationActionUninstall = "uninstall"
)

const (
	operationStatusRunning     = "running"
	operationStatusCompleted   = "completed"
	operationStatusFailed      = "failed"
	operationStatusInterrupted = "interrupted"
)

type operationResultSnapshot struct {
	Version     string `json:"version,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Status      string `json:"status,omitempty"`
	Health      string `json:"health,omitempty"`
}

type operationRecord struct {
	RequestID  string                   `json:"request_id"`
	Action     string                   `json:"action"`
	Module     string                   `json:"module"`
	Runtime    string                   `json:"runtime,omitempty"`
	Status     string                   `json:"status"`
	Stage      InstallStage             `json:"stage,omitempty"`
	StartedAt  string                   `json:"started_at"`
	UpdatedAt  string                   `json:"updated_at"`
	FinishedAt string                   `json:"finished_at,omitempty"`
	ErrorText  string                   `json:"error_text,omitempty"`
	Result     *operationResultSnapshot `json:"result,omitempty"`
}

type operationHandle struct {
	record operationRecord
	active bool
}

var (
	operationStateMu sync.Mutex
	moduleLocksMu    sync.Mutex
	moduleLocks      = map[string]string{}
)

func InitializeRuntimeState() error {
	operationStateMu.Lock()
	defer operationStateMu.Unlock()

	records, err := loadOperationRecords()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	changed := false
	for i := range records {
		if records[i].Status != operationStatusRunning {
			continue
		}
		records[i].Status = operationStatusInterrupted
		records[i].ErrorText = firstNonEmpty(strings.TrimSpace(records[i].ErrorText), "agent restarted before operation completed")
		records[i].UpdatedAt = now
		records[i].FinishedAt = now
		changed = true
	}
	if !changed {
		return nil
	}
	return saveOperationRecords(records)
}

func beginOperation(action string, module string, requestID string) (*operationHandle, *operationRecord, error) {
	module = normalizeModuleName(module)
	requestID = strings.TrimSpace(requestID)
	if module == "" {
		return nil, nil, fmt.Errorf("module is required")
	}
	if requestID == "" {
		return nil, nil, fmt.Errorf("request_id is required")
	}

	operationStateMu.Lock()
	defer operationStateMu.Unlock()

	records, err := loadOperationRecords()
	if err != nil {
		return nil, nil, err
	}
	for i := range records {
		if strings.TrimSpace(records[i].RequestID) != requestID {
			continue
		}
		existing := normalizeOperationRecord(records[i])
		if existing.Status == operationStatusRunning {
			return nil, &existing, fmt.Errorf("operation %s is already running", requestID)
		}
		return nil, &existing, nil
	}
	for i := range records {
		if normalizeModuleName(records[i].Module) != module {
			continue
		}
		if records[i].Status == operationStatusRunning {
			return nil, nil, fmt.Errorf("another operation is already running for module %s", module)
		}
	}

	if err := acquireModuleLock(module, requestID); err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := normalizeOperationRecord(operationRecord{
		RequestID: requestID,
		Action:    strings.TrimSpace(action),
		Module:    module,
		Runtime:   RuntimeLinuxSystemd,
		Status:    operationStatusRunning,
		Stage:     InstallStageValidate,
		StartedAt: now,
		UpdatedAt: now,
	})
	records = append(records, record)
	if err := saveOperationRecords(records); err != nil {
		releaseModuleLock(module, requestID)
		return nil, nil, err
	}
	return &operationHandle{record: record, active: true}, nil, nil
}

func (h *operationHandle) UpdateStage(stage InstallStage) {
	if h == nil || !h.active {
		return
	}
	operationStateMu.Lock()
	defer operationStateMu.Unlock()

	records, err := loadOperationRecords()
	if err != nil {
		return
	}
	for i := range records {
		if strings.TrimSpace(records[i].RequestID) != strings.TrimSpace(h.record.RequestID) {
			continue
		}
		records[i].Stage = stage
		records[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		h.record = normalizeOperationRecord(records[i])
		_ = saveOperationRecords(records)
		return
	}
}

func (h *operationHandle) Complete(result *operationResultSnapshot) {
	if h == nil || !h.active {
		return
	}
	defer h.release()
	operationStateMu.Lock()
	defer operationStateMu.Unlock()

	records, err := loadOperationRecords()
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range records {
		if strings.TrimSpace(records[i].RequestID) != strings.TrimSpace(h.record.RequestID) {
			continue
		}
		records[i].Status = operationStatusCompleted
		records[i].Stage = InstallStageCompleted
		records[i].Result = normalizeOperationResultSnapshot(result)
		records[i].ErrorText = ""
		records[i].UpdatedAt = now
		records[i].FinishedAt = now
		h.record = normalizeOperationRecord(records[i])
		_ = saveOperationRecords(records)
		return
	}
}

func (h *operationHandle) Fail(err error) {
	if h == nil || !h.active {
		return
	}
	defer h.release()
	operationStateMu.Lock()
	defer operationStateMu.Unlock()

	records, loadErr := loadOperationRecords()
	if loadErr != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := range records {
		if strings.TrimSpace(records[i].RequestID) != strings.TrimSpace(h.record.RequestID) {
			continue
		}
		records[i].Status = operationStatusFailed
		records[i].ErrorText = strings.TrimSpace(errorText(err))
		records[i].UpdatedAt = now
		records[i].FinishedAt = now
		h.record = normalizeOperationRecord(records[i])
		_ = saveOperationRecords(records)
		return
	}
}

func (h *operationHandle) release() {
	if h == nil || !h.active {
		return
	}
	releaseModuleLock(h.record.Module, h.record.RequestID)
	h.active = false
}

func loadOperationRecords() ([]operationRecord, error) {
	if _, err := os.Stat(installedOperationsStatePath); os.IsNotExist(err) {
		return []operationRecord{}, nil
	}
	raw, err := os.ReadFile(installedOperationsStatePath)
	if err != nil {
		return nil, fmt.Errorf("read operation journal failed: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return []operationRecord{}, nil
	}
	var items []operationRecord
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode operation journal failed: %w", err)
	}
	for i := range items {
		items[i] = normalizeOperationRecord(items[i])
	}
	return items, nil
}

func saveOperationRecords(items []operationRecord) error {
	for i := range items {
		items[i] = normalizeOperationRecord(items[i])
	}
	if err := os.MkdirAll(filepath.Dir(installedOperationsStatePath), 0o755); err != nil {
		return fmt.Errorf("create operation journal dir failed: %w", err)
	}
	body, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode operation journal failed: %w", err)
	}
	if err := os.WriteFile(installedOperationsStatePath, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write operation journal failed: %w", err)
	}
	return nil
}

func normalizeOperationRecord(record operationRecord) operationRecord {
	record.RequestID = strings.TrimSpace(record.RequestID)
	record.Action = strings.TrimSpace(record.Action)
	record.Module = normalizeModuleName(record.Module)
	record.Runtime = firstNonEmpty(strings.TrimSpace(record.Runtime), RuntimeLinuxSystemd)
	record.Status = normalizeOperationStatus(record.Status)
	record.StartedAt = strings.TrimSpace(record.StartedAt)
	record.UpdatedAt = strings.TrimSpace(record.UpdatedAt)
	record.FinishedAt = strings.TrimSpace(record.FinishedAt)
	record.ErrorText = strings.TrimSpace(record.ErrorText)
	record.Result = normalizeOperationResultSnapshot(record.Result)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if record.StartedAt == "" {
		record.StartedAt = now
	}
	if record.UpdatedAt == "" {
		record.UpdatedAt = record.StartedAt
	}
	return record
}

func normalizeOperationResultSnapshot(result *operationResultSnapshot) *operationResultSnapshot {
	if result == nil {
		return nil
	}
	out := &operationResultSnapshot{
		Version:     strings.TrimSpace(result.Version),
		ServiceName: strings.TrimSpace(result.ServiceName),
		Endpoint:    strings.TrimSpace(result.Endpoint),
		Status:      strings.TrimSpace(result.Status),
		Health:      normalizeInstalledModuleHealth(result.Health),
	}
	return out
}

func normalizeOperationStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", operationStatusRunning:
		return operationStatusRunning
	case operationStatusCompleted:
		return operationStatusCompleted
	case operationStatusFailed:
		return operationStatusFailed
	case operationStatusInterrupted:
		return operationStatusInterrupted
	default:
		return strings.TrimSpace(raw)
	}
}

func acquireModuleLock(module string, requestID string) error {
	moduleLocksMu.Lock()
	defer moduleLocksMu.Unlock()
	moduleKey := normalizeModuleName(module)
	if moduleKey == "" {
		return fmt.Errorf("module is required")
	}
	if existing, ok := moduleLocks[moduleKey]; ok && strings.TrimSpace(existing) != strings.TrimSpace(requestID) {
		return fmt.Errorf("module %s already has an active operation", moduleKey)
	}
	moduleLocks[moduleKey] = strings.TrimSpace(requestID)
	return nil
}

func releaseModuleLock(module string, requestID string) {
	moduleLocksMu.Lock()
	defer moduleLocksMu.Unlock()
	moduleKey := normalizeModuleName(module)
	if moduleKey == "" {
		return
	}
	if existing, ok := moduleLocks[moduleKey]; ok && strings.TrimSpace(existing) == strings.TrimSpace(requestID) {
		delete(moduleLocks, moduleKey)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
