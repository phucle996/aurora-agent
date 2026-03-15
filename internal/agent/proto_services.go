package agent

import (
	"aurora-agent/internal/agent/install"
	"context"
	agentcommandv1 "github.com/phucle996/aurora-proto/agentcommandv1"

	"google.golang.org/grpc"
)

type commandService struct {
	agentcommandv1.UnimplementedCommandServiceServer
}

func (s *commandService) RunCommand(
	ctx context.Context,
	req *agentcommandv1.RunCommandRequest,
) (*agentcommandv1.RunCommandResponse, error) {
	res, err := install.RunCommand(ctx, &install.RunCommandRequest{
		Command:        req.GetCommand(),
		TimeoutSeconds: req.GetTimeoutSeconds(),
		Env:            req.GetEnv(),
	})
	if err != nil {
		return nil, err
	}
	return &agentcommandv1.RunCommandResponse{
		Ok:        res.OK,
		ExitCode:  res.ExitCode,
		Output:    res.Output,
		ErrorText: res.ErrorText,
	}, nil
}

func (s *commandService) RunCommandStream(
	req *agentcommandv1.RunCommandRequest,
	stream grpc.ServerStreamingServer[agentcommandv1.RunCommandStreamEvent],
) error {
	return install.RunCommandStream(stream.Context(), req, func(event *agentcommandv1.RunCommandStreamEvent) error {
		return stream.Send(event)
	})
}

type installerService struct {
	agentcommandv1.UnimplementedInstallerServiceServer
}

func (s *installerService) InstallModule(
	ctx context.Context,
	req *agentcommandv1.InstallModuleRequest,
) (*agentcommandv1.InstallModuleResponse, error) {
	res, err := install.InstallModule(ctx, fromProtoInstallModuleRequest(req))
	if err != nil {
		return nil, err
	}
	return toProtoInstallModuleResponse(res), nil
}

func (s *installerService) InstallModuleStream(
	req *agentcommandv1.InstallModuleRequest,
	stream grpc.ServerStreamingServer[agentcommandv1.InstallModuleStreamEvent],
) error {
	return install.InstallModuleStream(stream.Context(), fromProtoInstallModuleRequest(req), func(event install.InstallModuleStreamEvent) error {
		return stream.Send(toProtoInstallModuleStreamEvent(event))
	})
}

func (s *installerService) RestartModule(
	ctx context.Context,
	req *agentcommandv1.RestartModuleRequest,
) (*agentcommandv1.RestartModuleResponse, error) {
	res, err := install.RestartModule(ctx, fromProtoRestartModuleRequest(req))
	if err != nil {
		return nil, err
	}
	return toProtoRestartModuleResponse(res), nil
}

func (s *installerService) UninstallModule(
	ctx context.Context,
	req *agentcommandv1.UninstallModuleRequest,
) (*agentcommandv1.UninstallModuleResponse, error) {
	res, err := install.UninstallModule(ctx, fromProtoUninstallModuleRequest(req))
	if err != nil {
		return nil, err
	}
	return toProtoUninstallModuleResponse(res), nil
}

func (s *installerService) ListInstalledModules(
	ctx context.Context,
	req *agentcommandv1.ListInstalledModulesRequest,
) (*agentcommandv1.ListInstalledModulesResponse, error) {
	res, err := install.ListInstalledModules(ctx, &install.ListInstalledModulesRequest{
		APIVersion: req.GetApiVersion(),
	})
	if err != nil {
		return nil, err
	}
	return toProtoListInstalledModulesResponse(res), nil
}

func fromProtoInstallModuleRequest(req *agentcommandv1.InstallModuleRequest) *install.InstallModuleRequest {
	if req == nil {
		return nil
	}
	return &install.InstallModuleRequest{
		APIVersion:       req.GetApiVersion(),
		RequestID:        req.GetRequestId(),
		Module:           req.GetModule(),
		Version:          req.GetVersion(),
		ArtifactURL:      req.GetArtifactUrl(),
		ArtifactChecksum: req.GetArtifactChecksum(),
		AppHost:          req.GetAppHost(),
		AppPort:          req.GetAppPort(),
		Env:              req.GetEnv(),
		Files:            req.GetFiles(),
	}
}

func fromProtoRestartModuleRequest(req *agentcommandv1.RestartModuleRequest) *install.RestartModuleRequest {
	if req == nil {
		return nil
	}
	return &install.RestartModuleRequest{
		APIVersion:  req.GetApiVersion(),
		RequestID:   req.GetRequestId(),
		Module:      req.GetModule(),
		ServiceName: req.GetServiceName(),
	}
}

func fromProtoUninstallModuleRequest(req *agentcommandv1.UninstallModuleRequest) *install.UninstallModuleRequest {
	if req == nil {
		return nil
	}
	return &install.UninstallModuleRequest{
		APIVersion:    req.GetApiVersion(),
		RequestID:     req.GetRequestId(),
		Module:        req.GetModule(),
		ServiceName:   req.GetServiceName(),
		UnitPath:      req.GetUnitPath(),
		BinaryPath:    req.GetBinaryPath(),
		EnvFilePath:   req.GetEnvFilePath(),
		NginxSitePath: req.GetNginxSitePath(),
		AssetPaths:    append([]string(nil), req.GetAssetPaths()...),
	}
}

func toProtoInstallLogEntries(items []install.InstallLogEntry) []*agentcommandv1.InstallLogEntry {
	out := make([]*agentcommandv1.InstallLogEntry, 0, len(items))
	for _, item := range items {
		out = append(out, &agentcommandv1.InstallLogEntry{
			Stage:   string(item.Stage),
			Message: item.Message,
		})
	}
	return out
}

func toProtoCapabilities(v install.ArtifactCapabilities) *agentcommandv1.ArtifactCapabilities {
	return &agentcommandv1.ArtifactCapabilities{
		Install:           v.Install,
		Restart:           v.Restart,
		Uninstall:         v.Uninstall,
		Migration:         v.Migration,
		AdminRpcBootstrap: v.AdminRPCBootstrap,
		NginxIntegration:  v.NginxIntegration,
	}
}

func toProtoInstallModuleResult(v *install.InstallModuleResult) *agentcommandv1.InstallModuleResult {
	if v == nil {
		return nil
	}
	return &agentcommandv1.InstallModuleResult{
		ApiVersion:            v.APIVersion,
		Module:                v.Module,
		Version:               v.Version,
		Runtime:               v.Runtime,
		ServiceName:           v.ServiceName,
		UnitPath:              v.UnitPath,
		BinaryPath:            v.BinaryPath,
		EnvFilePath:           v.EnvFilePath,
		NginxSitePath:         v.NginxSitePath,
		AssetPaths:            append([]string(nil), v.AssetPaths...),
		Endpoint:              v.Endpoint,
		Status:                v.Status,
		Health:                v.Health,
		ManifestSchemaVersion: v.ManifestSchemaVersion,
		Capabilities:          toProtoCapabilities(v.Capabilities),
	}
}

func toProtoInstallModuleResponse(v *install.InstallModuleResponse) *agentcommandv1.InstallModuleResponse {
	if v == nil {
		return &agentcommandv1.InstallModuleResponse{}
	}
	return &agentcommandv1.InstallModuleResponse{
		ApiVersion: v.APIVersion,
		Ok:         v.OK,
		Result:     toProtoInstallModuleResult(v.Result),
		Logs:       toProtoInstallLogEntries(v.Logs),
		ErrorText:  v.ErrorText,
	}
}

func toProtoInstallModuleStreamEvent(v install.InstallModuleStreamEvent) *agentcommandv1.InstallModuleStreamEvent {
	return &agentcommandv1.InstallModuleStreamEvent{
		ApiVersion: v.APIVersion,
		Type:       v.Type,
		Stage:      string(v.Stage),
		Message:    v.Message,
		Result:     toProtoInstallModuleResult(v.Result),
		ErrorText:  v.ErrorText,
	}
}

func toProtoRestartModuleResponse(v *install.RestartModuleResponse) *agentcommandv1.RestartModuleResponse {
	if v == nil {
		return &agentcommandv1.RestartModuleResponse{}
	}
	var result *agentcommandv1.RestartModuleResult
	if v.Result != nil {
		result = &agentcommandv1.RestartModuleResult{
			ApiVersion:  v.Result.APIVersion,
			Module:      v.Result.Module,
			Runtime:     v.Result.Runtime,
			ServiceName: v.Result.ServiceName,
			Status:      v.Result.Status,
			Health:      v.Result.Health,
		}
	}
	return &agentcommandv1.RestartModuleResponse{
		ApiVersion: v.APIVersion,
		Ok:         v.OK,
		Result:     result,
		Logs:       toProtoInstallLogEntries(v.Logs),
		ErrorText:  v.ErrorText,
	}
}

func toProtoUninstallModuleResponse(v *install.UninstallModuleResponse) *agentcommandv1.UninstallModuleResponse {
	if v == nil {
		return &agentcommandv1.UninstallModuleResponse{}
	}
	var result *agentcommandv1.UninstallModuleResult
	if v.Result != nil {
		result = &agentcommandv1.UninstallModuleResult{
			ApiVersion:  v.Result.APIVersion,
			Module:      v.Result.Module,
			Runtime:     v.Result.Runtime,
			ServiceName: v.Result.ServiceName,
			Status:      v.Result.Status,
			Health:      v.Result.Health,
		}
	}
	return &agentcommandv1.UninstallModuleResponse{
		ApiVersion: v.APIVersion,
		Ok:         v.OK,
		Result:     result,
		Logs:       toProtoInstallLogEntries(v.Logs),
		ErrorText:  v.ErrorText,
	}
}

func toProtoInstalledModuleRecord(v install.InstalledModuleRecord) *agentcommandv1.InstalledModuleRecord {
	return &agentcommandv1.InstalledModuleRecord{
		ApiVersion:            v.APIVersion,
		Module:                v.Module,
		Version:               v.Version,
		Runtime:               v.Runtime,
		ServiceName:           v.ServiceName,
		UnitPath:              v.UnitPath,
		BinaryPath:            v.BinaryPath,
		EnvFilePath:           v.EnvFilePath,
		NginxSitePath:         v.NginxSitePath,
		AssetPaths:            append([]string(nil), v.AssetPaths...),
		Endpoint:              v.Endpoint,
		Status:                v.Status,
		Health:                v.Health,
		ObservedAt:            v.ObservedAt,
		ManifestSchemaVersion: v.ManifestSchemaVersion,
		Capabilities:          toProtoCapabilities(v.Capabilities),
	}
}

func toProtoListInstalledModulesResponse(v *install.ListInstalledModulesResponse) *agentcommandv1.ListInstalledModulesResponse {
	if v == nil {
		return &agentcommandv1.ListInstalledModulesResponse{}
	}
	items := make([]*agentcommandv1.InstalledModuleRecord, 0, len(v.Items))
	for _, item := range v.Items {
		items = append(items, toProtoInstalledModuleRecord(item))
	}
	return &agentcommandv1.ListInstalledModulesResponse{
		ApiVersion: v.APIVersion,
		Ok:         v.OK,
		Items:      items,
		ErrorText:  v.ErrorText,
	}
}
