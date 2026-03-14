package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const installedModulesStatePath = "/var/lib/aurora-agent/installed_modules.json"

func listInstalledModules() ([]InstalledModuleRecord, error) {
	items, err := loadInstalledModules()
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Module == items[j].Module {
			return items[i].Runtime < items[j].Runtime
		}
		return items[i].Module < items[j].Module
	})
	return items, nil
}

func getInstalledModule(moduleName string, runtime string) (*InstalledModuleRecord, error) {
	items, err := loadInstalledModules()
	if err != nil {
		return nil, err
	}
	key := installedModuleKey(moduleName, runtime)
	for i := range items {
		if installedModuleKey(items[i].Module, items[i].Runtime) != key {
			continue
		}
		record := normalizeInstalledModuleRecord(items[i])
		return &record, nil
	}
	return nil, nil
}

func upsertInstalledModule(record InstalledModuleRecord) error {
	items, err := loadInstalledModules()
	if err != nil {
		return err
	}
	key := installedModuleKey(record.Module, record.Runtime)
	replaced := false
	for i := range items {
		if installedModuleKey(items[i].Module, items[i].Runtime) == key {
			items[i] = normalizeInstalledModuleRecord(record)
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, normalizeInstalledModuleRecord(record))
	}
	return saveInstalledModules(items)
}

func removeInstalledModule(moduleName string, runtime string) error {
	items, err := loadInstalledModules()
	if err != nil {
		return err
	}
	key := installedModuleKey(moduleName, runtime)
	filtered := items[:0]
	for _, item := range items {
		if installedModuleKey(item.Module, item.Runtime) == key {
			continue
		}
		filtered = append(filtered, item)
	}
	return saveInstalledModules(filtered)
}

func loadInstalledModules() ([]InstalledModuleRecord, error) {
	if _, err := os.Stat(installedModulesStatePath); os.IsNotExist(err) {
		return []InstalledModuleRecord{}, nil
	}
	raw, err := os.ReadFile(installedModulesStatePath)
	if err != nil {
		return nil, fmt.Errorf("read installed modules state failed: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return []InstalledModuleRecord{}, nil
	}
	var items []InstalledModuleRecord
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode installed modules state failed: %w", err)
	}
	for i := range items {
		items[i] = normalizeInstalledModuleRecord(items[i])
	}
	return items, nil
}

func saveInstalledModules(items []InstalledModuleRecord) error {
	for i := range items {
		items[i] = normalizeInstalledModuleRecord(items[i])
	}
	if err := os.MkdirAll(filepath.Dir(installedModulesStatePath), 0o755); err != nil {
		return fmt.Errorf("create installed modules state dir failed: %w", err)
	}
	body, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode installed modules state failed: %w", err)
	}
	if err := os.WriteFile(installedModulesStatePath, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write installed modules state failed: %w", err)
	}
	return nil
}

func normalizeInstalledModuleRecord(record InstalledModuleRecord) InstalledModuleRecord {
	record.Module = strings.TrimSpace(record.Module)
	record.APIVersion = normalizeInstallerAPIVersion(record.APIVersion)
	record.Version = strings.TrimSpace(record.Version)
	record.Runtime = strings.TrimSpace(record.Runtime)
	record.ServiceName = strings.TrimSpace(record.ServiceName)
	record.Endpoint = strings.TrimSpace(record.Endpoint)
	record.Status = normalizeInstalledModuleStatus(record.Status)
	record.Health = normalizeInstalledModuleHealth(record.Health)
	record.ObservedAt = strings.TrimSpace(record.ObservedAt)
	record.ManifestSchemaVersion = normalizeManifestSchemaVersion(record.ManifestSchemaVersion)
	record.Capabilities = normalizeArtifactCapabilities(record.Capabilities, nil)
	if record.ObservedAt == "" {
		record.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return record
}

func installedModuleKey(moduleName string, runtime string) string {
	return strings.TrimSpace(moduleName) + "|" + strings.TrimSpace(runtime)
}

func normalizeInstalledModuleStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", InstallStatusUnknown:
		return InstallStatusUnknown
	case "running", "restarted", "applied", InstallStatusInstalled:
		return InstallStatusInstalled
	case InstallStatusInstalling:
		return InstallStatusInstalling
	case InstallStatusFailed:
		return InstallStatusFailed
	default:
		return strings.TrimSpace(raw)
	}
}

func normalizeInstalledModuleHealth(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ModuleHealthUnknown:
		return ModuleHealthUnknown
	case ModuleHealthStarting:
		return ModuleHealthStarting
	case ModuleHealthHealthy:
		return ModuleHealthHealthy
	case ModuleHealthUnhealthy:
		return ModuleHealthUnhealthy
	case ModuleHealthDegraded:
		return ModuleHealthDegraded
	default:
		return strings.TrimSpace(raw)
	}
}
