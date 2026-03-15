package agent

import (
	agentcommandv1 "github.com/phucle996/aurora-proto/agentcommandv1"
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	agentversion "aurora-agent/internal/agent/version"
	"aurora-agent/internal/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	ggrpccreds "google.golang.org/grpc/credentials"
	ggrpcpeer "google.golang.org/grpc/peer"
	ggrpcstatus "google.golang.org/grpc/status"
)

const (
	agentRPCMethodGetVersion          = "/aurora.agent.v1.AgentService/GetVersion"
	agentRPCMethodCommandRun          = "/aurora.agent.v1.CommandService/RunCommand"
	agentRPCMethodCommandRunStream    = "/aurora.agent.v1.CommandService/RunCommandStream"
	agentRPCMethodInstallerInstall    = "/aurora.agent.v1.InstallerService/InstallModule"
	agentRPCMethodInstallerInstallStr = "/aurora.agent.v1.InstallerService/InstallModuleStream"
	agentRPCMethodInstallerRestart    = "/aurora.agent.v1.InstallerService/RestartModule"
	agentRPCMethodInstallerUninstall  = "/aurora.agent.v1.InstallerService/UninstallModule"
	agentRPCMethodInstallerList       = "/aurora.agent.v1.InstallerService/ListInstalledModules"
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
	agentcommandv1.RegisterCommandServiceServer(server, &commandService{})
	agentcommandv1.RegisterInstallerServiceServer(server, &installerService{})

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
		agentRPCMethodCommandRun,
		agentRPCMethodCommandRunStream:
		return nil
	case agentRPCMethodInstallerInstall,
		agentRPCMethodInstallerInstallStr,
		agentRPCMethodInstallerRestart,
		agentRPCMethodInstallerUninstall,
		agentRPCMethodInstallerList:
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

type agentServiceServer interface {
	GetVersion(context.Context, *agentversion.GetVersionRequest) (*agentversion.GetVersionResponse, error)
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
	},
	Streams:  []grpc.StreamDesc{},
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
