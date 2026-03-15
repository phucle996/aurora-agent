package adminrpc

import (
	"aurora-agent/internal/config"
	runtimev1 "github.com/phucle996/aurora-proto/runtimev1"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	defaultInvokeTimeout    = 8 * time.Second
	certRenewCheckInterval  = 1 * time.Minute
	hostRoutingSyncInterval = 1 * time.Minute
)

type HeartbeatPayload struct {
	AgentID           string
	Hostname          string
	AgentVersion      string
	AgentProbeAddr    string
	AgentGRPCEndpoint string
	Platform          string
	Architecture      string
}

type HeartbeatClient struct {
	logger             *slog.Logger
	cfg                config.Config
	target             string
	inferredServerName string

	connMu sync.RWMutex
	conn   *gogrpc.ClientConn

	reconnectMu sync.Mutex

	certRenewMu     sync.Mutex
	lastCertCheckAt time.Time

	hostRoutingMu    sync.Mutex
	lastHostSyncAt   time.Time
	lastHostSyncHash string
}

type MetricsPolicy struct {
	StreamEnabled        bool
	BatchFlushInterval   time.Duration
	BatchSampleInterval  time.Duration
	StreamSampleInterval time.Duration
	MaxBatchRecords      int
}

func NewHeartbeatClient(cfg config.Config, logger *slog.Logger) (*HeartbeatClient, error) {
	target, inferredServerName, err := normalizeAdminRPCTarget(cfg.AdminGRPCAddr)
	if err != nil {
		return nil, err
	}

	if err := ensureAgentClientCertificate(cfg, target, inferredServerName, logger, false); err != nil {
		return nil, err
	}

	client := &HeartbeatClient{
		logger:             logger,
		cfg:                cfg,
		target:             target,
		inferredServerName: inferredServerName,
	}
	if err := client.reconnect(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *HeartbeatClient) Close() error {
	if c == nil {
		return nil
	}
	c.connMu.Lock()
	conn := c.conn
	c.conn = nil
	c.connMu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (c *HeartbeatClient) reconnect() error {
	if c == nil {
		return fmt.Errorf("heartbeat client is nil")
	}

	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	tlsCfg, err := c.cfg.AdminTLSConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(tlsCfg.ServerName) == "" {
		tlsCfg.ServerName = c.inferredServerName
	}

	conn, err := gogrpc.NewClient(c.target, gogrpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return fmt.Errorf("dial admin rpc failed: %w", err)
	}

	c.connMu.Lock()
	old := c.conn
	c.conn = conn
	c.connMu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (c *HeartbeatClient) currentConn() *gogrpc.ClientConn {
	if c == nil {
		return nil
	}
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *HeartbeatClient) runtimeClient(conn *gogrpc.ClientConn) runtimev1.RuntimeServiceClient {
	if conn == nil {
		return nil
	}
	return runtimev1.NewRuntimeServiceClient(conn)
}

func normalizeAdminRPCTarget(endpoint string) (target string, serverName string, err error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return "", "", fmt.Errorf("admin grpc endpoint is empty")
	}

	if strings.Contains(raw, "://") {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid admin grpc endpoint %q", endpoint)
		}
		scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
		switch scheme {
		case "https", "grpcs", "tls":
		default:
			return "", "", fmt.Errorf("admin grpc endpoint must use tls")
		}

		host := strings.TrimSpace(parsed.Hostname())
		if host == "" {
			return "", "", fmt.Errorf("cannot resolve admin server name from endpoint %q", endpoint)
		}
		port := strings.TrimSpace(parsed.Port())
		if port == "" {
			return "", "", fmt.Errorf("admin grpc endpoint must include explicit port (direct gRPC), got %q", endpoint)
		}
		return net.JoinHostPort(host, port), host, nil
	}

	host, port, splitErr := net.SplitHostPort(raw)
	if splitErr != nil {
		return "", "", fmt.Errorf("admin grpc endpoint must include explicit port (direct gRPC), got %q", endpoint)
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return "", "", fmt.Errorf("cannot resolve admin server name from endpoint %q", endpoint)
	}
	if strings.TrimSpace(port) == "" {
		return "", "", fmt.Errorf("admin grpc endpoint port is empty in %q", endpoint)
	}
	return net.JoinHostPort(host, port), host, nil
}
