package agent

import (
	"aurora-agent/internal/agent/install"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	agentversion "aurora-agent/internal/agent/version"
	"aurora-agent/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	ggrpccreds "google.golang.org/grpc/credentials"
	ggrpcencoding "google.golang.org/grpc/encoding"
	ggrpcpeer "google.golang.org/grpc/peer"
	ggrpcstatus "google.golang.org/grpc/status"
)

const (
	agentRPCMethodGetVersion          = "/aurora.agent.v1.AgentService/GetVersion"
	agentRPCMethodRunCommand          = "/aurora.agent.v1.AgentService/RunCommand"
	agentRPCMethodInstallModule       = "/aurora.agent.v1.AgentService/InstallModule"
	agentRPCMethodInstallModuleStream = "/aurora.agent.v1.AgentService/InstallModuleStream"
	agentRPCMethodRestartModule       = "/aurora.agent.v1.AgentService/RestartModule"
	agentRPCMethodUninstallModule     = "/aurora.agent.v1.AgentService/UninstallModule"
	agentRPCMethodListInstalled       = "/aurora.agent.v1.AgentService/ListInstalledModules"
)

func (a *Agent) runProbeListener(ctx context.Context) error {
	addr := a.cfg.ProbeListenAddr
	if addr == "" {
		return fmt.Errorf("empty probe listen address")
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen probe endpoint %s: %w", addr, err)
	}
	defer func() { _ = ln.Close() }()

	a.logger.Info("probe endpoint listening", "addr", addr)

	registerAgentJSONCodec()
	tlsCfg, tlsErr := a.cfg.ProbeServerTLSConfig()
	if tlsErr != nil {
		return fmt.Errorf("probe tls config failed: %w", tlsErr)
	}
	server := grpc.NewServer(
		grpc.Creds(ggrpccreds.NewTLS(tlsCfg)),
		grpc.UnaryInterceptor(authorizeAdminClientInterceptor(a.cfg)),
		grpc.StreamInterceptor(authorizeAdminClientStreamInterceptor(a.cfg)),
	)
	registerAgentServiceServer(server, &agentService{
		cfg:    a.cfg,
		logger: a.logger,
	})

	go func() {
		<-ctx.Done()
		done := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			server.Stop()
		}
		_ = ln.Close()
	}()

	if err := server.Serve(ln); err != nil && ctx.Err() == nil {
		return fmt.Errorf("serve probe rpc endpoint %s: %w", addr, err)
	}
	return nil
}

func authorizeAdminClientInterceptor(cfg config.Config) grpc.UnaryServerInterceptor {
	expectedCN := strings.TrimSpace(cfg.AdminClientCN)
	expectedServiceID := strings.TrimSpace(strings.ToLower(cfg.AdminClientServiceID))
	expectedRole := strings.TrimSpace(strings.ToLower(cfg.AdminClientRole))
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if err := authorizeAdminClientFromContext(ctx, info.FullMethod, expectedCN, expectedServiceID, expectedRole); err != nil {
			return nil, ggrpcstatus.Error(codes.PermissionDenied, err.Error())
		}
		return handler(ctx, req)
	}
}

func authorizeAdminClientStreamInterceptor(cfg config.Config) grpc.StreamServerInterceptor {
	expectedCN := strings.TrimSpace(cfg.AdminClientCN)
	expectedServiceID := strings.TrimSpace(strings.ToLower(cfg.AdminClientServiceID))
	expectedRole := strings.TrimSpace(strings.ToLower(cfg.AdminClientRole))
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if err := authorizeAdminClientFromContext(ss.Context(), info.FullMethod, expectedCN, expectedServiceID, expectedRole); err != nil {
			return ggrpcstatus.Error(codes.PermissionDenied, err.Error())
		}
		return handler(srv, ss)
	}
}

func authorizeAdminClientFromContext(ctx context.Context, fullMethod string, expectedCN string, expectedServiceID string, expectedRole string) error {
	if err := authorizeAdminMethod(fullMethod); err != nil {
		return err
	}
	peerInfo, ok := ggrpcpeer.FromContext(ctx)
	if !ok || peerInfo == nil || peerInfo.AuthInfo == nil {
		return fmt.Errorf("missing peer auth info")
	}
	tlsInfo, ok := peerInfo.AuthInfo.(ggrpccreds.TLSInfo)
	if !ok {
		return fmt.Errorf("peer is not authenticated with tls")
	}
	if len(tlsInfo.State.PeerCertificates) == 0 {
		return fmt.Errorf("missing peer certificate")
	}
	cert := tlsInfo.State.PeerCertificates[0]
	claims := readIdentityClaimsFromCertificateURIs(cert)

	if expectedServiceID != "" || expectedRole != "" {
		if expectedServiceID != "" {
			serviceID := strings.TrimSpace(strings.ToLower(claims.ServiceID))
			if serviceID == "" {
				return fmt.Errorf("missing service_id claim in certificate")
			}
			if serviceID != expectedServiceID {
				return fmt.Errorf("unauthorized service")
			}
		}
		if expectedRole != "" {
			role := strings.TrimSpace(strings.ToLower(claims.Role))
			if role == "" {
				return fmt.Errorf("missing role claim in certificate")
			}
			if role != expectedRole {
				return fmt.Errorf("unauthorized role")
			}
		}
		return nil
	}

	if expectedCN != "" {
		if strings.EqualFold(strings.TrimSpace(cert.Subject.CommonName), expectedCN) {
			return nil
		}
		for _, dnsName := range cert.DNSNames {
			if strings.EqualFold(strings.TrimSpace(dnsName), expectedCN) {
				return nil
			}
		}
		return fmt.Errorf("unauthorized client certificate")
	}
	return nil
}

func authorizeAdminMethod(fullMethod string) error {
	switch strings.TrimSpace(fullMethod) {
	case agentRPCMethodGetVersion,
		agentRPCMethodRunCommand,
		agentRPCMethodInstallModule,
		agentRPCMethodInstallModuleStream,
		agentRPCMethodRestartModule,
		agentRPCMethodUninstallModule,
		agentRPCMethodListInstalled:
		return nil
	default:
		return fmt.Errorf("method is not allowed")
	}
}

type probePeerIdentityClaims struct {
	ServiceID string
	Role      string
}

func readIdentityClaimsFromCertificateURIs(cert *x509.Certificate) probePeerIdentityClaims {
	claims := probePeerIdentityClaims{}
	if cert == nil {
		return claims
	}
	for _, uri := range cert.URIs {
		if uri == nil || !strings.EqualFold(strings.TrimSpace(uri.Scheme), "spiffe") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(uri.Host), "aurora.local") {
			continue
		}
		pathParts := strings.Split(strings.Trim(strings.TrimSpace(uri.Path), "/"), "/")
		if len(pathParts) != 2 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(pathParts[0])) {
		case "service":
			claims.ServiceID = strings.TrimSpace(pathParts[1])
		case "role":
			claims.Role = strings.TrimSpace(pathParts[1])
		}
	}
	return claims
}

type agentService struct {
	cfg    config.Config
	logger *slog.Logger
}

func (s *agentService) GetVersion(ctx context.Context, req *agentversion.GetVersionRequest) (*agentversion.GetVersionResponse, error) {
	_ = ctx
	return agentversion.Get(s.cfg, req), nil
}

func (s *agentService) RunCommand(ctx context.Context, req *install.RunCommandRequest) (*install.RunCommandResponse, error) {
	return install.RunCommand(ctx, req)
}

func (s *agentService) InstallModule(ctx context.Context, req *install.InstallModuleRequest) (*install.InstallModuleResponse, error) {
	return install.InstallModule(ctx, req)
}

func (s *agentService) InstallModuleStream(req *install.InstallModuleRequest, stream agentInstallModuleStreamServer) error {
	return install.InstallModuleStream(stream.Context(), req, func(event install.InstallModuleStreamEvent) error {
		return stream.Send(&event)
	})
}

func (s *agentService) RestartModule(ctx context.Context, req *install.RestartModuleRequest) (*install.RestartModuleResponse, error) {
	return install.RestartModule(ctx, req)
}

func (s *agentService) UninstallModule(ctx context.Context, req *install.UninstallModuleRequest) (*install.UninstallModuleResponse, error) {
	return install.UninstallModule(ctx, req)
}

func (s *agentService) ListInstalledModules(ctx context.Context, req *install.ListInstalledModulesRequest) (*install.ListInstalledModulesResponse, error) {
	return install.ListInstalledModules(ctx, req)
}

type jsonCodec struct{}

func (jsonCodec) Name() string {
	return "json"
}

func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

var registerAgentCodecOnce sync.Once

func registerAgentJSONCodec() {
	registerAgentCodecOnce.Do(func() {
		ggrpcencoding.RegisterCodec(jsonCodec{})
	})
}

type agentServiceServer interface {
	GetVersion(context.Context, *agentversion.GetVersionRequest) (*agentversion.GetVersionResponse, error)
	RunCommand(context.Context, *install.RunCommandRequest) (*install.RunCommandResponse, error)
	InstallModule(context.Context, *install.InstallModuleRequest) (*install.InstallModuleResponse, error)
	InstallModuleStream(*install.InstallModuleRequest, agentInstallModuleStreamServer) error
	RestartModule(context.Context, *install.RestartModuleRequest) (*install.RestartModuleResponse, error)
	UninstallModule(context.Context, *install.UninstallModuleRequest) (*install.UninstallModuleResponse, error)
	ListInstalledModules(context.Context, *install.ListInstalledModulesRequest) (*install.ListInstalledModulesResponse, error)
}

type agentInstallModuleStreamServer interface {
	Send(*install.InstallModuleStreamEvent) error
	grpc.ServerStream
}

func registerAgentServiceServer(s grpc.ServiceRegistrar, srv agentServiceServer) {
	s.RegisterService(&agentServiceDesc, srv)
}

var agentServiceDesc = grpc.ServiceDesc{
	ServiceName: "aurora.agent.v1.AgentService",
	HandlerType: (*agentServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetVersion",
			Handler:    _agentServiceGetVersionHandler,
		},
		{
			MethodName: "RunCommand",
			Handler:    _agentServiceRunCommandHandler,
		},
		{
			MethodName: "InstallModule",
			Handler:    _agentServiceInstallModuleHandler,
		},
		{
			MethodName: "RestartModule",
			Handler:    _agentServiceRestartModuleHandler,
		},
		{
			MethodName: "UninstallModule",
			Handler:    _agentServiceUninstallModuleHandler,
		},
		{
			MethodName: "ListInstalledModules",
			Handler:    _agentServiceListInstalledModulesHandler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "InstallModuleStream",
			Handler:       _agentServiceInstallModuleStreamHandler,
			ServerStreams: true,
		},
	},
	Metadata: "agent_service.proto",
}

func _agentServiceGetVersionHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(agentversion.GetVersionRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(agentServiceServer).GetVersion(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/aurora.agent.v1.AgentService/GetVersion",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(agentServiceServer).GetVersion(ctx, req.(*agentversion.GetVersionRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _agentServiceRunCommandHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(install.RunCommandRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(agentServiceServer).RunCommand(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/aurora.agent.v1.AgentService/RunCommand",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(agentServiceServer).RunCommand(ctx, req.(*install.RunCommandRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _agentServiceInstallModuleHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(install.InstallModuleRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(agentServiceServer).InstallModule(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/aurora.agent.v1.AgentService/InstallModule",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(agentServiceServer).InstallModule(ctx, req.(*install.InstallModuleRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _agentServiceInstallModuleStreamHandler(srv any, stream grpc.ServerStream) error {
	in := new(install.InstallModuleRequest)
	if err := stream.RecvMsg(in); err != nil {
		return err
	}
	return srv.(agentServiceServer).InstallModuleStream(in, &agentInstallModuleStreamServerImpl{ServerStream: stream})
}

type agentInstallModuleStreamServerImpl struct {
	grpc.ServerStream
}

func (s *agentInstallModuleStreamServerImpl) Send(event *install.InstallModuleStreamEvent) error {
	return s.ServerStream.SendMsg(event)
}

func _agentServiceRestartModuleHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(install.RestartModuleRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(agentServiceServer).RestartModule(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/aurora.agent.v1.AgentService/RestartModule",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(agentServiceServer).RestartModule(ctx, req.(*install.RestartModuleRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _agentServiceUninstallModuleHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(install.UninstallModuleRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(agentServiceServer).UninstallModule(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/aurora.agent.v1.AgentService/UninstallModule",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(agentServiceServer).UninstallModule(ctx, req.(*install.UninstallModuleRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _agentServiceListInstalledModulesHandler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(install.ListInstalledModulesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(agentServiceServer).ListInstalledModules(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/aurora.agent.v1.AgentService/ListInstalledModules",
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(agentServiceServer).ListInstalledModules(ctx, req.(*install.ListInstalledModulesRequest))
	}
	return interceptor(ctx, in, info, handler)
}
