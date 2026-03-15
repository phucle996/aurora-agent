package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func InstallModule(ctx context.Context, req *InstallModuleRequest) (*InstallModuleResponse, error) {
	if req == nil {
		return &InstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			ErrorText:  "install request is required",
		}, nil
	}
	var versionErr error
	req.APIVersion, versionErr = validateInstallerAPIVersion(req.APIVersion)
	if versionErr != nil {
		return &InstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedInstallResult(*req),
			ErrorText:  versionErr.Error(),
		}, nil
	}
	handle, replay, err := beginOperation(operationActionInstall, req.Module, req.RequestID)
	if err != nil {
		return &InstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedInstallResult(*req),
			ErrorText:  err.Error(),
		}, nil
	}
	if replay != nil {
		return replayInstallModuleResponse(*req, *replay), nil
	}

	logs := make([]InstallLogEntry, 0, 16)
	result, err := executeInstallModule(ctx, *req, func(stage InstallStage, message string) {
		logs = append(logs, InstallLogEntry{
			Stage:   stage,
			Message: strings.TrimSpace(message),
		})
		handle.UpdateStage(stage)
	})
	if err != nil {
		handle.Fail(err)
		errorText := redactInstallText(err.Error(), *req)
		return &InstallModuleResponse{
			OK:        false,
			Result:    failedInstallResult(*req),
			Logs:      logs,
			ErrorText: errorText,
		}, nil
	}
	handle.Complete(&operationResultSnapshot{
		Version:     result.Version,
		ServiceName: result.ServiceName,
		Endpoint:    result.Endpoint,
		Status:      result.Status,
		Health:      result.Health,
	})

	return &InstallModuleResponse{
		APIVersion: InstallerRPCVersionV1,
		OK:         true,
		Result:     result,
		Logs:       logs,
	}, nil
}

func InstallModuleStream(
	ctx context.Context,
	req *InstallModuleRequest,
	send func(InstallModuleStreamEvent) error,
) error {
	if req == nil {
		return send(InstallModuleStreamEvent{
			APIVersion: InstallerRPCVersionV1,
			Type:       "error",
			ErrorText:  "install request is required",
		})
	}
	if send == nil {
		return fmt.Errorf("install stream sender is required")
	}
	var versionErr error
	req.APIVersion, versionErr = validateInstallerAPIVersion(req.APIVersion)
	if versionErr != nil {
		return send(InstallModuleStreamEvent{
			APIVersion: InstallerRPCVersionV1,
			Type:       "error",
			Result:     failedInstallResult(*req),
			ErrorText:  versionErr.Error(),
		})
	}
	handle, replay, err := beginOperation(operationActionInstall, req.Module, req.RequestID)
	if err != nil {
		return send(InstallModuleStreamEvent{
			APIVersion: InstallerRPCVersionV1,
			Type:       "error",
			Result:     failedInstallResult(*req),
			ErrorText:  err.Error(),
		})
	}
	if replay != nil {
		replayRes := replayInstallModuleResponse(*req, *replay)
		if replayRes.OK {
			return send(InstallModuleStreamEvent{
				APIVersion: InstallerRPCVersionV1,
				Type:       "result",
				Result:     replayRes.Result,
				Message:    "operation replayed from journal",
			})
		}
		return send(InstallModuleStreamEvent{
			APIVersion: InstallerRPCVersionV1,
			Type:       "error",
			Result:     replayRes.Result,
			ErrorText:  replayRes.ErrorText,
		})
	}
	result, err := executeInstallModule(ctx, *req, func(stage InstallStage, message string) {
		handle.UpdateStage(stage)
		_ = send(InstallModuleStreamEvent{
			APIVersion: InstallerRPCVersionV1,
			Type:       "log",
			Stage:      stage,
			Message:    strings.TrimSpace(message),
		})
	})
	if err != nil {
		handle.Fail(err)
		return send(InstallModuleStreamEvent{
			APIVersion: InstallerRPCVersionV1,
			Type:       "error",
			Result:     failedInstallResult(*req),
			ErrorText:  redactInstallText(err.Error(), *req),
		})
	}
	handle.Complete(&operationResultSnapshot{
		Version:     result.Version,
		ServiceName: result.ServiceName,
		Endpoint:    result.Endpoint,
		Status:      result.Status,
		Health:      result.Health,
	})
	return send(InstallModuleStreamEvent{
		APIVersion: InstallerRPCVersionV1,
		Type:       "result",
		Result:     result,
		Message:    "module install completed",
	})
}

func executeInstallModule(
	ctx context.Context,
	req InstallModuleRequest,
	onLog func(stage InstallStage, message string),
) (*InstallModuleResult, error) {
	engine := NewEngine()
	redactedLogFn := func(stage InstallStage, message string) {
		if onLog == nil {
			return
		}
		onLog(stage, redactInstallText(message, req))
	}
	result, err := engine.InstallModule(ctx, req, redactedLogFn)
	if err != nil {
		errorText := redactInstallText(err.Error(), req)
		_ = appendAuditEvent(auditEvent{
			Action:           "install",
			RequestID:        strings.TrimSpace(req.RequestID),
			Module:           strings.TrimSpace(req.Module),
			Version:          strings.TrimSpace(req.Version),
			Runtime:          RuntimeLinuxSystemd,
			ArtifactURL:      strings.TrimSpace(req.ArtifactURL),
			ArtifactChecksum: strings.TrimSpace(req.ArtifactChecksum),
			Status:           InstallStatusFailed,
			OK:               false,
			ErrorText:        errorText,
		})
		return nil, fmt.Errorf("%s", errorText)
	}

	_ = upsertInstalledModule(InstalledModuleRecord{
		APIVersion:            InstallerRPCVersionV1,
		Module:                result.Module,
		Version:               result.Version,
		Runtime:               result.Runtime,
		ServiceName:           result.ServiceName,
		UnitPath:              result.UnitPath,
		BinaryPath:            result.BinaryPath,
		EnvFilePath:           result.EnvFilePath,
		NginxSitePath:         result.NginxSitePath,
		Endpoint:              result.Endpoint,
		Status:                result.Status,
		Health:                result.Health,
		ObservedAt:            time.Now().UTC().Format(time.RFC3339Nano),
		ManifestSchemaVersion: result.ManifestSchemaVersion,
		Capabilities:          result.Capabilities,
	})
	_ = appendAuditEvent(auditEvent{
		Action:           "install",
		RequestID:        strings.TrimSpace(req.RequestID),
		Module:           result.Module,
		Version:          result.Version,
		Runtime:          result.Runtime,
		ArtifactURL:      strings.TrimSpace(req.ArtifactURL),
		ArtifactChecksum: strings.TrimSpace(req.ArtifactChecksum),
		Status:           result.Status,
		OK:               true,
	})
	return result, nil
}

func RestartModule(ctx context.Context, req *RestartModuleRequest) (*RestartModuleResponse, error) {
	if req == nil {
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			ErrorText:  "restart request is required",
		}, nil
	}
	var versionErr error
	req.APIVersion, versionErr = validateInstallerAPIVersion(req.APIVersion)
	if versionErr != nil {
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedRestartResult(*req, RuntimeLinuxSystemd),
			ErrorText:  versionErr.Error(),
		}, nil
	}
	handle, replay, err := beginOperation(operationActionRestart, req.Module, req.RequestID)
	if err != nil {
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedRestartResult(*req, RuntimeLinuxSystemd),
			ErrorText:  err.Error(),
		}, nil
	}
	if replay != nil {
		return replayRestartModuleResponse(*req, *replay), nil
	}

	runtime := RuntimeLinuxSystemd
	record, recordErr := getInstalledModule(strings.TrimSpace(req.Module), runtime)
	if recordErr != nil {
		handle.Fail(recordErr)
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedRestartResult(*req, runtime),
			ErrorText:  strings.TrimSpace(recordErr.Error()),
		}, nil
	}
	if record != nil && !record.Capabilities.Restart {
		err := fmt.Errorf("module restart is not allowed by installed artifact capabilities")
		handle.Fail(err)
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedRestartResult(*req, runtime),
			ErrorText:  err.Error(),
		}, nil
	}

	serviceName := strings.TrimSpace(req.ServiceName)
	if serviceName == "" {
		handle.Fail(fmt.Errorf("service_name is required"))
		_ = appendAuditEvent(auditEvent{
			Action:    "restart",
			RequestID: strings.TrimSpace(req.RequestID),
			Module:    strings.TrimSpace(req.Module),
			Runtime:   runtime,
			Status:    InstallStatusFailed,
			OK:        false,
			ErrorText: "service_name is required",
		})
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedRestartResult(*req, runtime),
			ErrorText:  "service_name is required",
		}, nil
	}

	logs := make([]InstallLogEntry, 0, 4)
	logs = append(logs, InstallLogEntry{Stage: InstallStageApply, Message: "restarting systemd service"})
	handle.UpdateStage(InstallStageApply)
	if err := runSystemctl(ctx, "restart", serviceName); err != nil {
		handle.Fail(err)
		_ = appendAuditEvent(auditEvent{
			Action:    "restart",
			RequestID: strings.TrimSpace(req.RequestID),
			Module:    strings.TrimSpace(req.Module),
			Runtime:   runtime,
			Status:    InstallStatusFailed,
			OK:        false,
			ErrorText: strings.TrimSpace(err.Error()),
		})
		logs = append(logs, InstallLogEntry{Stage: InstallStageApply, Message: strings.TrimSpace(err.Error())})
		return &RestartModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedRestartResult(*req, runtime),
			Logs:       logs,
			ErrorText:  strings.TrimSpace(err.Error()),
		}, nil
	}
	logs = append(logs, InstallLogEntry{Stage: InstallStageHealth, Message: "checking systemd service health"})
	handle.UpdateStage(InstallStageHealth)
	if err := checkSystemdServiceActive(ctx, execCommandRunner{}, serviceName); err != nil {
		handle.Fail(err)
		_ = appendAuditEvent(auditEvent{
			Action:    "restart",
			RequestID: strings.TrimSpace(req.RequestID),
			Module:    strings.TrimSpace(req.Module),
			Runtime:   runtime,
			Status:    InstallStatusFailed,
			OK:        false,
			ErrorText: strings.TrimSpace(err.Error()),
		})
		logs = append(logs, InstallLogEntry{Stage: InstallStageHealth, Message: strings.TrimSpace(err.Error())})
		return &RestartModuleResponse{
			OK:        false,
			Result:    failedRestartResult(*req, runtime),
			Logs:      logs,
			ErrorText: strings.TrimSpace(err.Error()),
		}, nil
	}

	result := &RestartModuleResult{
		APIVersion:  InstallerRPCVersionV1,
		Module:      strings.TrimSpace(req.Module),
		Runtime:     runtime,
		ServiceName: serviceName,
		Status:      "restarted",
		Health:      ModuleHealthHealthy,
	}
	_ = upsertInstalledModule(InstalledModuleRecord{
		APIVersion:  InstallerRPCVersionV1,
		Module:      strings.TrimSpace(req.Module),
		Runtime:     runtime,
		ServiceName: serviceName,
		UnitPath: func() string {
			if record != nil {
				return record.UnitPath
			}
			return ""
		}(),
		BinaryPath: func() string {
			if record != nil {
				return record.BinaryPath
			}
			return ""
		}(),
		EnvFilePath: func() string {
			if record != nil {
				return record.EnvFilePath
			}
			return ""
		}(),
		NginxSitePath: func() string {
			if record != nil {
				return record.NginxSitePath
			}
			return ""
		}(),
		Status:      InstallStatusInstalled,
		Health:      ModuleHealthHealthy,
		ObservedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Capabilities: func() ArtifactCapabilities {
			if record != nil {
				return record.Capabilities
			}
			return normalizeArtifactCapabilities(ArtifactCapabilities{}, nil)
		}(),
		ManifestSchemaVersion: func() string {
			if record != nil {
				return record.ManifestSchemaVersion
			}
			return ""
		}(),
	})
	_ = appendAuditEvent(auditEvent{
		Action:    "restart",
		RequestID: strings.TrimSpace(req.RequestID),
		Module:    strings.TrimSpace(req.Module),
		Runtime:   runtime,
		Status:    result.Status,
		OK:        true,
	})
	handle.Complete(&operationResultSnapshot{
		ServiceName: serviceName,
		Status:      result.Status,
		Health:      result.Health,
	})
	logs = append(logs, InstallLogEntry{Stage: InstallStageCompleted, Message: "module restart completed"})
	return &RestartModuleResponse{
		APIVersion: InstallerRPCVersionV1,
		OK:         true,
		Result:     result,
		Logs:       logs,
	}, nil
}

func UninstallModule(ctx context.Context, req *UninstallModuleRequest) (*UninstallModuleResponse, error) {
	if req == nil {
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			ErrorText:  "uninstall request is required",
		}, nil
	}
	var versionErr error
	req.APIVersion, versionErr = validateInstallerAPIVersion(req.APIVersion)
	if versionErr != nil {
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedUninstallResult(*req, RuntimeLinuxSystemd),
			ErrorText:  versionErr.Error(),
		}, nil
	}
	handle, replay, err := beginOperation(operationActionUninstall, req.Module, req.RequestID)
	if err != nil {
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedUninstallResult(*req, RuntimeLinuxSystemd),
			ErrorText:  err.Error(),
		}, nil
	}
	if replay != nil {
		return replayUninstallModuleResponse(*req, *replay), nil
	}

	runtime := RuntimeLinuxSystemd
	record, recordErr := getInstalledModule(strings.TrimSpace(req.Module), runtime)
	if recordErr != nil {
		handle.Fail(recordErr)
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedUninstallResult(*req, runtime),
			ErrorText:  strings.TrimSpace(recordErr.Error()),
		}, nil
	}
	if record != nil && !record.Capabilities.Uninstall {
		err := fmt.Errorf("module uninstall is not allowed by installed artifact capabilities")
		handle.Fail(err)
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedUninstallResult(*req, runtime),
			ErrorText:  err.Error(),
		}, nil
	}

	serviceName := strings.TrimSpace(req.ServiceName)
	if serviceName == "" {
		handle.Fail(fmt.Errorf("service_name is required"))
		_ = appendAuditEvent(auditEvent{
			Action:    "uninstall",
			RequestID: strings.TrimSpace(req.RequestID),
			Module:    strings.TrimSpace(req.Module),
			Runtime:   runtime,
			Status:    InstallStatusFailed,
			OK:        false,
			ErrorText: "service_name is required",
		})
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedUninstallResult(*req, runtime),
			ErrorText:  "service_name is required",
		}, nil
	}

	logs := make([]InstallLogEntry, 0, 8)
	appendLog := func(stage InstallStage, message string) {
		logs = append(logs, InstallLogEntry{Stage: stage, Message: strings.TrimSpace(message)})
		handle.UpdateStage(stage)
	}

	unitPath := strings.TrimSpace(req.UnitPath)
	if unitPath == "" {
		unitPath = filepath.Join("/etc/systemd/system", serviceName)
	}

	appendLog(InstallStageApply, "stopping systemd service")
	_ = runSystemctl(ctx, "stop", serviceName)
	appendLog(InstallStageApply, "disabling systemd service")
	_ = runSystemctl(ctx, "disable", serviceName)

	if err := removePathIfExists(unitPath); err != nil {
		handle.Fail(err)
		_ = appendAuditEvent(auditEvent{
			Action:    "uninstall",
			RequestID: strings.TrimSpace(req.RequestID),
			Module:    strings.TrimSpace(req.Module),
			Runtime:   runtime,
			Status:    InstallStatusFailed,
			OK:        false,
			ErrorText: strings.TrimSpace(err.Error()),
		})
		appendLog(InstallStageApply, err.Error())
		return &UninstallModuleResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			Result:     failedUninstallResult(*req, runtime),
			Logs:       logs,
			ErrorText:  strings.TrimSpace(err.Error()),
		}, nil
	}

	if err := runSystemctl(ctx, "daemon-reload"); err != nil {
		handle.Fail(err)
		_ = appendAuditEvent(auditEvent{
			Action:    "uninstall",
			RequestID: strings.TrimSpace(req.RequestID),
			Module:    strings.TrimSpace(req.Module),
			Runtime:   runtime,
			Status:    InstallStatusFailed,
			OK:        false,
			ErrorText: strings.TrimSpace(err.Error()),
		})
		appendLog(InstallStageApply, err.Error())
		return &UninstallModuleResponse{
			OK:        false,
			Result:    failedUninstallResult(*req, runtime),
			Logs:      logs,
			ErrorText: strings.TrimSpace(err.Error()),
		}, nil
	}

	for _, path := range []string{
		strings.TrimSpace(req.BinaryPath),
		strings.TrimSpace(req.EnvFilePath),
		strings.TrimSpace(req.NginxSitePath),
	} {
		if path == "" {
			continue
		}
		if err := removePathIfExists(path); err != nil {
			_ = appendAuditEvent(auditEvent{
				Action:    "uninstall",
				Module:    strings.TrimSpace(req.Module),
				Runtime:   runtime,
				Status:    InstallStatusFailed,
				OK:        false,
				ErrorText: strings.TrimSpace(err.Error()),
			})
			appendLog(InstallStageApply, err.Error())
			return &UninstallModuleResponse{
				APIVersion: InstallerRPCVersionV1,
				OK:         false,
				Result:     failedUninstallResult(*req, runtime),
				Logs:       logs,
				ErrorText:  strings.TrimSpace(err.Error()),
			}, nil
		}
	}

	if sitePath := strings.TrimSpace(req.NginxSitePath); sitePath != "" {
		appendLog(InstallStageApply, "reloading nginx")
		if err := reloadNginx(ctx); err != nil {
			handle.Fail(err)
			_ = appendAuditEvent(auditEvent{
				Action:    "uninstall",
				RequestID: strings.TrimSpace(req.RequestID),
				Module:    strings.TrimSpace(req.Module),
				Runtime:   runtime,
				Status:    InstallStatusFailed,
				OK:        false,
				ErrorText: strings.TrimSpace(err.Error()),
			})
			appendLog(InstallStageApply, err.Error())
			return &UninstallModuleResponse{
				APIVersion: InstallerRPCVersionV1,
				OK:         false,
				Result:     failedUninstallResult(*req, runtime),
				Logs:       logs,
				ErrorText:  strings.TrimSpace(err.Error()),
			}, nil
		}
	}

	result := &UninstallModuleResult{
		APIVersion:  InstallerRPCVersionV1,
		Module:      strings.TrimSpace(req.Module),
		Runtime:     runtime,
		ServiceName: serviceName,
		Status:      "uninstalled",
		Health:      ModuleHealthUnknown,
	}
	_ = removeInstalledModule(strings.TrimSpace(req.Module), runtime)
	_ = appendAuditEvent(auditEvent{
		Action:    "uninstall",
		RequestID: strings.TrimSpace(req.RequestID),
		Module:    strings.TrimSpace(req.Module),
		Runtime:   runtime,
		Status:    result.Status,
		OK:        true,
	})
	handle.Complete(&operationResultSnapshot{
		ServiceName: serviceName,
		Status:      result.Status,
		Health:      result.Health,
	})
	appendLog(InstallStageCompleted, "module uninstall completed")
	return &UninstallModuleResponse{
		APIVersion: InstallerRPCVersionV1,
		OK:         true,
		Result:     result,
		Logs:       logs,
	}, nil
}

func ListInstalledModules(_ context.Context, req *ListInstalledModulesRequest) (*ListInstalledModulesResponse, error) {
	if req == nil {
		req = &ListInstalledModulesRequest{}
	}
	var versionErr error
	req.APIVersion, versionErr = validateInstallerAPIVersion(req.APIVersion)
	if versionErr != nil {
		return &ListInstalledModulesResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			ErrorText:  versionErr.Error(),
		}, nil
	}
	items, err := listInstalledModules()
	if err != nil {
		return &ListInstalledModulesResponse{
			APIVersion: InstallerRPCVersionV1,
			OK:         false,
			ErrorText:  strings.TrimSpace(err.Error()),
		}, nil
	}
	return &ListInstalledModulesResponse{
		APIVersion: InstallerRPCVersionV1,
		OK:         true,
		Items:      items,
	}, nil
}

func failedInstallResult(req InstallModuleRequest) *InstallModuleResult {
	return &InstallModuleResult{
		APIVersion:   InstallerRPCVersionV1,
		Module:       strings.TrimSpace(req.Module),
		Version:      strings.TrimSpace(req.Version),
		Runtime:      RuntimeLinuxSystemd,
		Status:       InstallStatusFailed,
		Health:       ModuleHealthUnknown,
		Capabilities: normalizeArtifactCapabilities(ArtifactCapabilities{}, nil),
	}
}

func failedRestartResult(req RestartModuleRequest, runtime string) *RestartModuleResult {
	return &RestartModuleResult{
		APIVersion:  InstallerRPCVersionV1,
		Module:      strings.TrimSpace(req.Module),
		Runtime:     runtime,
		ServiceName: strings.TrimSpace(req.ServiceName),
		Status:      InstallStatusFailed,
		Health:      ModuleHealthUnknown,
	}
}

func failedUninstallResult(req UninstallModuleRequest, runtime string) *UninstallModuleResult {
	return &UninstallModuleResult{
		APIVersion:  InstallerRPCVersionV1,
		Module:      strings.TrimSpace(req.Module),
		Runtime:     runtime,
		ServiceName: strings.TrimSpace(req.ServiceName),
		Status:      InstallStatusFailed,
		Health:      ModuleHealthUnknown,
	}
}

func replayInstallModuleResponse(req InstallModuleRequest, record operationRecord) *InstallModuleResponse {
	result := failedInstallResult(req)
	if record.Result != nil {
		result.Version = strings.TrimSpace(record.Result.Version)
		result.ServiceName = strings.TrimSpace(record.Result.ServiceName)
		result.Endpoint = strings.TrimSpace(record.Result.Endpoint)
		result.Status = firstNonEmpty(strings.TrimSpace(record.Result.Status), result.Status)
		result.Health = firstNonEmpty(strings.TrimSpace(record.Result.Health), result.Health)
	}
	if record.Status == operationStatusCompleted {
		return &InstallModuleResponse{OK: true, Result: result}
	}
	return &InstallModuleResponse{
		OK:        false,
		Result:    result,
		ErrorText: strings.TrimSpace(record.ErrorText),
	}
}

func replayRestartModuleResponse(req RestartModuleRequest, record operationRecord) *RestartModuleResponse {
	result := failedRestartResult(req, RuntimeLinuxSystemd)
	if record.Result != nil {
		result.ServiceName = firstNonEmpty(strings.TrimSpace(record.Result.ServiceName), result.ServiceName)
		result.Status = firstNonEmpty(strings.TrimSpace(record.Result.Status), result.Status)
		result.Health = firstNonEmpty(strings.TrimSpace(record.Result.Health), result.Health)
	}
	if record.Status == operationStatusCompleted {
		return &RestartModuleResponse{OK: true, Result: result}
	}
	return &RestartModuleResponse{
		OK:        false,
		Result:    result,
		ErrorText: strings.TrimSpace(record.ErrorText),
	}
}

func replayUninstallModuleResponse(req UninstallModuleRequest, record operationRecord) *UninstallModuleResponse {
	result := failedUninstallResult(req, RuntimeLinuxSystemd)
	if record.Result != nil {
		result.ServiceName = firstNonEmpty(strings.TrimSpace(record.Result.ServiceName), result.ServiceName)
		result.Status = firstNonEmpty(strings.TrimSpace(record.Result.Status), result.Status)
		result.Health = firstNonEmpty(strings.TrimSpace(record.Result.Health), result.Health)
	}
	if record.Status == operationStatusCompleted {
		return &UninstallModuleResponse{OK: true, Result: result}
	}
	return &UninstallModuleResponse{
		OK:        false,
		Result:    result,
		ErrorText: strings.TrimSpace(record.ErrorText),
	}
}

func runSystemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		text = strings.TrimSpace(err.Error())
	}
	return fmt.Errorf("systemctl %s failed: %s", strings.Join(args, " "), text)
}

func removePathIfExists(path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if err := os.Remove(trimmed); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s failed: %w", trimmed, err)
	}
	return nil
}

func reloadNginx(ctx context.Context) error {
	if err := runExec(ctx, "nginx", "-t"); err != nil {
		return err
	}
	if err := runSystemctl(ctx, "reload", "nginx"); err != nil {
		return err
	}
	return nil
}

func runExec(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		text = strings.TrimSpace(err.Error())
	}
	return fmt.Errorf("%s failed: %s", strings.Join(append([]string{name}, args...), " "), text)
}
