package adminrpc

import (
	"aurora-agent/internal/config"
	"aurora-agent/internal/model"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	runtimeReportAgentHeartbeatPath  = "/admin.transport.runtime.v1.RuntimeService/ReportAgentHeartbeat"
	runtimeBootstrapAgentPath        = "/admin.transport.runtime.v1.RuntimeService/BootstrapAgent"
	runtimeRenewAgentCertPath        = "/admin.transport.runtime.v1.RuntimeService/RenewAgentCertificate"
	runtimeGetAgentMetricsPolicyPath = "/admin.transport.runtime.v1.RuntimeService/GetAgentMetricsPolicy"
	runtimeReportAgentMetricsPath    = "/admin.transport.runtime.v1.RuntimeService/ReportAgentMetrics"
	defaultInvokeTimeout             = 8 * time.Second
	certRenewCheckInterval           = 1 * time.Minute
)

type HeartbeatPayload struct {
	AgentID           string
	Hostname          string
	AgentVersion      string
	AgentProbeAddr    string
	AgentGRPCEndpoint string
	Platform          string
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

func ensureAgentClientCertificate(
	cfg config.Config,
	target string,
	inferredServerName string,
	logger *slog.Logger,
	force bool,
) error {
	if !force && certAndKeyExists(cfg.AdminTLSCertPath, cfg.AdminTLSKeyPath) {
		return nil
	}
	if strings.TrimSpace(cfg.BootstrapToken) == "" {
		return fmt.Errorf("missing agent client cert/key and bootstrap token")
	}

	generatedKeyPEM, csrPEM, err := generateAgentKeyAndCSR(cfg)
	if err != nil {
		return err
	}

	bootstrapTLS, err := buildBootstrapTLSConfig(cfg, inferredServerName)
	if err != nil {
		return err
	}
	conn, err := gogrpc.NewClient(target, gogrpc.WithTransportCredentials(credentials.NewTLS(bootstrapTLS)))
	if err != nil {
		return fmt.Errorf("dial admin bootstrap rpc failed: %w", err)
	}
	defer conn.Close()

	callCtx, cancel := context.WithTimeout(context.Background(), defaultInvokeTimeout)
	defer cancel()
	req, err := structpb.NewStruct(map[string]any{
		"node_id":             strings.TrimSpace(cfg.NodeID),
		"cluster_id":          strings.TrimSpace(cfg.ClusterID),
		"service_id":          strings.TrimSpace(cfg.ServiceID),
		"role":                strings.TrimSpace(cfg.Role),
		"hostname":            strings.TrimSpace(cfg.Hostname),
		"ip":                  strings.TrimSpace(cfg.AgentIP),
		"bootstrap_token":     strings.TrimSpace(cfg.BootstrapToken),
		"csr_pem":             strings.TrimSpace(string(csrPEM)),
		"agent_probe_addr":    strings.TrimSpace(cfg.ProbeListenAddr),
		"agent_grpc_endpoint": strings.TrimSpace(cfg.AgentGRPCEndpoint),
		"platform":            strings.TrimSpace(cfg.Platform),
	})
	if err != nil {
		return fmt.Errorf("build bootstrap request failed: %w", err)
	}
	resp := &structpb.Struct{}
	if err := conn.Invoke(callCtx, runtimeBootstrapAgentPath, req, resp); err != nil {
		return fmt.Errorf("bootstrap agent rpc failed: %w", err)
	}

	clientCertPEM := strings.TrimSpace(readStructString(resp, "client_cert_pem"))
	caCertPEM := strings.TrimSpace(readStructString(resp, "ca_cert_pem"))
	if clientCertPEM == "" || caCertPEM == "" {
		return fmt.Errorf("bootstrap rpc response missing certificates")
	}
	clientKeyPEM := strings.TrimSpace(string(generatedKeyPEM))
	if clientKeyPEM == "" {
		return fmt.Errorf("generated client key is empty")
	}

	if err := writeSecureFile(cfg.AdminTLSKeyPath, []byte(clientKeyPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent tls key failed: %w", err)
	}
	if err := writeSecureFile(cfg.AdminTLSCertPath, []byte(clientCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent tls cert failed: %w", err)
	}
	if err := writeSecureFile(cfg.AdminServerCAPath, []byte(caCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write admin ca cert failed: %w", err)
	}
	if logger != nil {
		if force {
			logger.Info("agent bootstrap certificate rotated")
		} else {
			logger.Info("agent bootstrap certificate completed")
		}
	}
	return nil
}

func buildBootstrapTLSConfig(cfg config.Config, inferredServerName string) (*tls.Config, error) {
	caPath := strings.TrimSpace(cfg.AdminServerCAPath)
	if caPath == "" {
		return nil, fmt.Errorf("AURORA_ADMIN_SERVER_CA_PATH is required")
	}
	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read admin ca file failed: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("invalid admin ca pem")
	}

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}
	if serverName := strings.TrimSpace(cfg.AdminServerName); serverName != "" {
		tlsCfg.ServerName = serverName
	} else {
		tlsCfg.ServerName = inferredServerName
	}
	return tlsCfg, nil
}

func certAndKeyExists(certPath string, keyPath string) bool {
	certPath = strings.TrimSpace(certPath)
	keyPath = strings.TrimSpace(keyPath)
	if certPath == "" || keyPath == "" {
		return false
	}
	if _, err := os.Stat(certPath); err != nil {
		return false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	return true
}

func generateAgentKeyAndCSR(cfg config.Config) ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate agent private key failed: %w", err)
	}

	dnsNames := uniqueStrings([]string{
		strings.TrimSpace(cfg.NodeID),
		strings.TrimSpace(cfg.Hostname),
	})
	ipAddresses := make([]net.IP, 0, 1)
	if ip := net.ParseIP(strings.TrimSpace(cfg.AgentIP)); ip != nil {
		ipAddresses = append(ipAddresses, ip)
	}
	uris := buildAgentIdentityURIs(cfg)

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "agent:" + strings.TrimSpace(cfg.NodeID),
		},
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
		URIs:        uris,
	}, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create csr failed: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	if len(keyPEM) == 0 || len(csrPEM) == 0 {
		return nil, nil, fmt.Errorf("encode csr/key pem failed")
	}
	return keyPEM, csrPEM, nil
}

func buildAgentIdentityURIs(cfg config.Config) []*url.URL {
	values := []string{
		fmt.Sprintf("spiffe://aurora.local/node/%s", strings.TrimSpace(cfg.NodeID)),
		fmt.Sprintf("spiffe://aurora.local/service/%s", strings.TrimSpace(cfg.ServiceID)),
		fmt.Sprintf("spiffe://aurora.local/role/%s", strings.TrimSpace(cfg.Role)),
		fmt.Sprintf("spiffe://aurora.local/cluster/%s", strings.TrimSpace(cfg.ClusterID)),
	}
	out := make([]*url.URL, 0, len(values))
	for _, raw := range values {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		out = append(out, parsed)
	}
	return out
}

func writeSecureFile(path string, content []byte, perm os.FileMode) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, perm); err != nil {
		return err
	}
	return nil
}

func readStructString(req *structpb.Struct, key string) string {
	if req == nil {
		return ""
	}
	field, ok := req.GetFields()[key]
	if !ok || field == nil {
		return ""
	}
	return strings.TrimSpace(field.GetStringValue())
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
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

func (c *HeartbeatClient) ReportHeartbeat(ctx context.Context, payload HeartbeatPayload) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("heartbeat client is nil")
	}

	req, err := structpb.NewStruct(map[string]any{
		"agent_id":            strings.TrimSpace(payload.AgentID),
		"hostname":            strings.TrimSpace(payload.Hostname),
		"agent_version":       strings.TrimSpace(payload.AgentVersion),
		"agent_probe_addr":    strings.TrimSpace(payload.AgentProbeAddr),
		"agent_grpc_endpoint": strings.TrimSpace(payload.AgentGRPCEndpoint),
		"platform":            strings.TrimSpace(payload.Platform),
	})
	if err != nil {
		return fmt.Errorf("build heartbeat request failed: %w", err)
	}

	callCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, defaultInvokeTimeout)
		defer cancel()
	}

	resp := &structpb.Struct{}
	if err := c.invokeWithRecovery(callCtx, runtimeReportAgentHeartbeatPath, req, resp); err != nil {
		return fmt.Errorf("report heartbeat failed: %w", err)
	}
	if c.logger != nil {
		c.logger.Debug("agent heartbeat acknowledged by admin")
	}
	return nil
}

func (c *HeartbeatClient) GetMetricsPolicy(ctx context.Context, agentID string) (MetricsPolicy, error) {
	if c == nil || c.conn == nil {
		return MetricsPolicy{}, fmt.Errorf("heartbeat client is nil")
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultInvokeTimeout)
	defer cancel()
	req, err := structpb.NewStruct(map[string]any{
		"agent_id": strings.TrimSpace(agentID),
	})
	if err != nil {
		return MetricsPolicy{}, err
	}
	resp := &structpb.Struct{}
	if err := c.invokeWithRecovery(callCtx, runtimeGetAgentMetricsPolicyPath, req, resp); err != nil {
		return MetricsPolicy{}, err
	}

	defaultPolicy := MetricsPolicy{
		StreamEnabled:        false,
		BatchFlushInterval:   3 * time.Minute,
		BatchSampleInterval:  10 * time.Second,
		StreamSampleInterval: 3 * time.Second,
		MaxBatchRecords:      2048,
	}
	p := defaultPolicy
	p.StreamEnabled = readStructBool(resp, "stream_enabled", defaultPolicy.StreamEnabled)
	p.BatchFlushInterval = readStructSecondsAsDuration(resp, "batch_flush_interval_seconds", defaultPolicy.BatchFlushInterval)
	p.BatchSampleInterval = readStructSecondsAsDuration(resp, "batch_sample_interval_seconds", defaultPolicy.BatchSampleInterval)
	p.StreamSampleInterval = readStructSecondsAsDuration(resp, "stream_sample_interval_seconds", defaultPolicy.StreamSampleInterval)
	p.MaxBatchRecords = int(readStructNumber(resp, "max_batch_records", float64(defaultPolicy.MaxBatchRecords)))
	if p.MaxBatchRecords <= 0 {
		p.MaxBatchRecords = defaultPolicy.MaxBatchRecords
	}
	return p, nil
}

func (c *HeartbeatClient) ReportMetrics(
	ctx context.Context,
	agentID string,
	mode string,
	records []model.AgentBasicMetricRecord,
) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	if len(records) == 0 {
		return nil
	}

	list := make([]any, 0, len(records))
	for _, record := range records {
		services := make([]any, 0, len(record.Services))
		for _, serviceRecord := range record.Services {
			services = append(services, map[string]any{
				"service":               serviceRecord.Service,
				"cpu_usage_percent":     serviceRecord.CPUUsagePercent,
				"memory_used_bytes":     serviceRecord.MemoryUsedBytes,
				"disk_read_bps":         serviceRecord.DiskReadBps,
				"disk_write_bps":        serviceRecord.DiskWriteBps,
				"network_rx_bps":        serviceRecord.NetworkRxBps,
				"network_tx_bps":        serviceRecord.NetworkTxBps,
				"gpu_util_percent":      serviceRecord.GPUUtilPercent,
				"gpu_memory_used_bytes": serviceRecord.GPUMemoryUsedBytes,
			})
		}

		list = append(list, map[string]any{
			"ts_ms":              record.TimestampUnixMillis,
			"cpu_usage_percent":  record.CPUUsagePercent,
			"memory_used_bytes":  record.MemoryUsedBytes,
			"memory_total_bytes": record.MemoryTotalBytes,
			"disk_read_bps":      record.DiskReadBps,
			"disk_write_bps":     record.DiskWriteBps,
			"network_rx_bps":     record.NetworkRxBps,
			"network_tx_bps":     record.NetworkTxBps,
			"gpu": map[string]any{
				"count":              record.GPU.Count,
				"util_percent":       record.GPU.UtilPercent,
				"memory_used_bytes":  record.GPU.MemoryUsedBytes,
				"memory_total_bytes": record.GPU.MemoryTotalBytes,
			},
			"services":       services,
			"uptime_seconds": record.UptimeSeconds,
		})
	}

	req, err := structpb.NewStruct(map[string]any{
		"agent_id": strings.TrimSpace(agentID),
		"mode":     strings.TrimSpace(strings.ToLower(mode)),
		"records":  list,
	})
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultInvokeTimeout)
	defer cancel()
	resp := &structpb.Struct{}
	if err := c.invokeWithRecovery(callCtx, runtimeReportAgentMetricsPath, req, resp); err != nil {
		return err
	}
	return nil
}

func (c *HeartbeatClient) invokeWithRecovery(
	ctx context.Context,
	method string,
	req *structpb.Struct,
	resp *structpb.Struct,
) error {
	if c == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	if renewErr := c.maybeRenewClientCertificate(ctx); renewErr != nil && c.logger != nil {
		c.logger.Warn("preflight certificate renewal check failed", "error", renewErr)
	}
	conn := c.currentConn()
	if conn == nil {
		if err := c.reconnect(); err != nil {
			return classifyAdminRPCError(err, c.cfg.AdminServerCAPath)
		}
		conn = c.currentConn()
		if conn == nil {
			return fmt.Errorf("admin rpc connection is unavailable")
		}
	}

	err := conn.Invoke(ctx, method, req, resp)
	if err == nil {
		return nil
	}

	classified := classifyAdminRPCError(err, c.cfg.AdminServerCAPath)
	if !isRecoverableAdminRPCError(err) {
		return classified
	}

	if reconnectErr := c.reconnect(); reconnectErr != nil {
		return fmt.Errorf("%w; reconnect failed: %v", classified, classifyAdminRPCError(reconnectErr, c.cfg.AdminServerCAPath))
	}
	retryConn := c.currentConn()
	if retryConn == nil {
		return fmt.Errorf("%w; reconnect failed: connection unavailable", classified)
	}
	retryErr := retryConn.Invoke(ctx, method, req, resp)
	if retryErr == nil {
		return nil
	}
	retryClassified := classifyAdminRPCError(retryErr, c.cfg.AdminServerCAPath)

	if shouldTryBootstrapRotation(retryErr, c.cfg.BootstrapToken) {
		if c.logger != nil {
			c.logger.Warn("admin rpc failed; trying agent cert rotation via bootstrap token")
		}
		if rotateErr := ensureAgentClientCertificate(c.cfg, c.target, c.inferredServerName, c.logger, true); rotateErr == nil {
			if reconnectErr := c.reconnect(); reconnectErr == nil {
				lastConn := c.currentConn()
				if lastConn != nil {
					lastErr := lastConn.Invoke(ctx, method, req, resp)
					if lastErr == nil {
						return nil
					}
					return classifyAdminRPCError(lastErr, c.cfg.AdminServerCAPath)
				}
			}
		} else if c.logger != nil {
			c.logger.Warn("agent cert rotation via bootstrap token failed", "error", rotateErr)
		}
	}

	return retryClassified
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

func (c *HeartbeatClient) maybeRenewClientCertificate(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("heartbeat client is nil")
	}
	now := time.Now().UTC()

	c.certRenewMu.Lock()
	defer c.certRenewMu.Unlock()
	if !c.lastCertCheckAt.IsZero() && now.Sub(c.lastCertCheckAt) < certRenewCheckInterval {
		return nil
	}
	c.lastCertCheckAt = now

	leaf, err := loadLeafCertificate(c.cfg.AdminTLSCertPath)
	if err != nil {
		return err
	}
	renewBefore := calculateRenewBeforeWindow(leaf)
	if now.Before(leaf.NotAfter.Add(-renewBefore)) {
		return nil
	}

	if c.logger != nil {
		c.logger.Info(
			"client certificate is nearing expiry; renewing via mTLS",
			"not_after", leaf.NotAfter.UTC().Format(time.RFC3339),
			"renew_before", renewBefore.String(),
		)
	}
	return c.renewClientCertificate(ctx)
}

func (c *HeartbeatClient) renewClientCertificate(ctx context.Context) error {
	keyPEM, csrPEM, err := generateAgentKeyAndCSR(c.cfg)
	if err != nil {
		return err
	}

	callCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, defaultInvokeTimeout)
		defer cancel()
	}

	conn := c.currentConn()
	if conn == nil {
		if err := c.reconnect(); err != nil {
			return err
		}
		conn = c.currentConn()
		if conn == nil {
			return fmt.Errorf("admin rpc connection is unavailable")
		}
	}

	req, err := structpb.NewStruct(map[string]any{
		"csr_pem":    strings.TrimSpace(string(csrPEM)),
		"hostname":   strings.TrimSpace(c.cfg.Hostname),
		"ip":         strings.TrimSpace(c.cfg.AgentIP),
		"cluster_id": strings.TrimSpace(c.cfg.ClusterID),
	})
	if err != nil {
		return err
	}
	resp := &structpb.Struct{}
	if err := conn.Invoke(callCtx, runtimeRenewAgentCertPath, req, resp); err != nil {
		return err
	}

	clientCertPEM := strings.TrimSpace(readStructString(resp, "client_cert_pem"))
	caCertPEM := strings.TrimSpace(readStructString(resp, "ca_cert_pem"))
	if clientCertPEM == "" || caCertPEM == "" {
		return fmt.Errorf("renew certificate response missing cert chain")
	}
	if err := writeSecureFile(c.cfg.AdminTLSKeyPath, []byte(strings.TrimSpace(string(keyPEM))+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed tls key failed: %w", err)
	}
	if err := writeSecureFile(c.cfg.AdminTLSCertPath, []byte(clientCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed tls cert failed: %w", err)
	}
	if err := writeSecureFile(c.cfg.AdminServerCAPath, []byte(caCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed tls ca failed: %w", err)
	}
	if err := c.reconnect(); err != nil {
		return err
	}
	if c.logger != nil {
		c.logger.Info("client certificate renewal completed")
	}
	return nil
}

func loadLeafCertificate(certPath string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(strings.TrimSpace(certPath))
	if err != nil {
		return nil, fmt.Errorf("read tls cert failed: %w", err)
	}
	rest := raw
	for len(rest) > 0 {
		block, next := pem.Decode(rest)
		rest = next
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return nil, parseErr
		}
		return cert, nil
	}
	return nil, fmt.Errorf("no certificate block found")
}

func calculateRenewBeforeWindow(cert *x509.Certificate) time.Duration {
	if cert == nil {
		return 24 * time.Hour
	}
	lifetime := cert.NotAfter.Sub(cert.NotBefore)
	if lifetime <= 0 {
		return 24 * time.Hour
	}
	window := lifetime / 5
	if window < 6*time.Hour {
		window = 6 * time.Hour
	}
	if window > 72*time.Hour {
		window = 72 * time.Hour
	}
	return window
}

func shouldTryBootstrapRotation(err error, bootstrapToken string) bool {
	if strings.TrimSpace(bootstrapToken) == "" || err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "certificate signed by unknown authority") {
		return false
	}
	if strings.Contains(msg, "authentication handshake failed") {
		return true
	}
	if strings.Contains(msg, "certificate required") ||
		strings.Contains(msg, "bad certificate") ||
		strings.Contains(msg, "certificate revoked") ||
		strings.Contains(msg, "certificate has expired") ||
		strings.Contains(msg, "permission denied") {
		return true
	}
	return false
}

func isRecoverableAdminRPCError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection error") ||
		strings.Contains(msg, "transport is closing") ||
		strings.Contains(msg, "authentication handshake failed") ||
		strings.Contains(msg, "code = unavailable")
}

func classifyAdminRPCError(err error, caPath string) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unexpected http status code received from server: 404") ||
		strings.Contains(msg, "unexpected content-type \"application/json") {
		return fmt.Errorf("admin grpc endpoint is not the direct gRPC port (likely nginx/http endpoint): %w", err)
	}
	if strings.Contains(msg, "certificate signed by unknown authority") {
		return fmt.Errorf(
			"admin tls verification failed (ca mismatch at %s); update agent CA file to current Aurora Admin CA and retry: %w",
			strings.TrimSpace(caPath),
			err,
		)
	}
	return err
}

func readStructBool(req *structpb.Struct, key string, fallback bool) bool {
	if req == nil {
		return fallback
	}
	field, ok := req.GetFields()[key]
	if !ok || field == nil {
		return fallback
	}
	switch v := field.GetKind().(type) {
	case *structpb.Value_BoolValue:
		return v.BoolValue
	case *structpb.Value_NumberValue:
		return v.NumberValue > 0
	case *structpb.Value_StringValue:
		switch strings.ToLower(strings.TrimSpace(v.StringValue)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func readStructNumber(req *structpb.Struct, key string, fallback float64) float64 {
	if req == nil {
		return fallback
	}
	field, ok := req.GetFields()[key]
	if !ok || field == nil {
		return fallback
	}
	n := field.GetNumberValue()
	if n <= 0 {
		return fallback
	}
	return n
}

func readStructSecondsAsDuration(req *structpb.Struct, key string, fallback time.Duration) time.Duration {
	v := readStructNumber(req, key, 0)
	if v <= 0 {
		return fallback
	}
	return time.Duration(v) * time.Second
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
