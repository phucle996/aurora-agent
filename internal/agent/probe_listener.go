package agent

import (
	"aurora-agent/internal/agent/install"
	"context"
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
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if err := authorizeAdminClientFromContext(ctx, expectedCN); err != nil {
			return nil, ggrpcstatus.Error(codes.PermissionDenied, err.Error())
		}
		return handler(ctx, req)
	}
}

func authorizeAdminClientFromContext(ctx context.Context, expectedCN string) error {
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
	if expectedCN == "" {
		return nil
	}
	cert := tlsInfo.State.PeerCertificates[0]
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
