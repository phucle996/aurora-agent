package adminrpc

import (
	"aurora-agent/internal/config"
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

	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/structpb"
)

func ensureAgentClientCertificate(
	cfg config.Config,
	target string,
	inferredServerName string,
	logger *slog.Logger,
	force bool,
) error {
	if !force && certAndKeyExists(cfg.AdminTLSClientCertPath, cfg.AdminTLSClientKeyPath) &&
		certAndKeyExists(cfg.AgentTLSServerCertPath, cfg.AgentTLSServerKeyPath) {
		return nil
	}
	if strings.TrimSpace(cfg.BootstrapToken) == "" {
		return fmt.Errorf("missing agent tls materials and bootstrap token")
	}

	clientKeyPEM, clientCSRPEM, err := generateAgentClientKeyAndCSR(cfg)
	if err != nil {
		return err
	}
	serverKeyPEM, serverCSRPEM, err := generateAgentServerKeyAndCSR(cfg)
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
		"csr_pem":             strings.TrimSpace(string(clientCSRPEM)),
		"server_csr_pem":      strings.TrimSpace(string(serverCSRPEM)),
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
	serverCertPEM := strings.TrimSpace(readStructString(resp, "server_cert_pem"))
	adminServerCAPEM := strings.TrimSpace(readStructString(resp, "admin_server_ca_pem"))
	if clientCertPEM == "" || serverCertPEM == "" || adminServerCAPEM == "" {
		return fmt.Errorf("bootstrap rpc response missing certificates")
	}
	clientKeyMaterial := strings.TrimSpace(string(clientKeyPEM))
	serverKeyMaterial := strings.TrimSpace(string(serverKeyPEM))
	if clientKeyMaterial == "" || serverKeyMaterial == "" {
		return fmt.Errorf("generated client key is empty")
	}

	if err := writeSecureFile(cfg.AdminTLSClientKeyPath, []byte(clientKeyMaterial+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent client tls key failed: %w", err)
	}
	if err := writeSecureFile(cfg.AdminTLSClientCertPath, []byte(clientCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent client tls cert failed: %w", err)
	}
	if err := writeSecureFile(cfg.AgentTLSServerKeyPath, []byte(serverKeyMaterial+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent serving tls key failed: %w", err)
	}
	if err := writeSecureFile(cfg.AgentTLSServerCertPath, []byte(serverCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent serving tls cert failed: %w", err)
	}
	if err := writeSecureFile(cfg.AdminServerCAPath, []byte(adminServerCAPEM+"\n"), 0o600); err != nil {
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

func generateAgentClientKeyAndCSR(cfg config.Config) ([]byte, []byte, error) {
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

func generateAgentServerKeyAndCSR(cfg config.Config) ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate agent server private key failed: %w", err)
	}

	dnsNames := uniqueStrings([]string{
		strings.TrimSpace(cfg.NodeID),
		strings.TrimSpace(cfg.Hostname),
		serverEndpointHost(cfg.AgentGRPCEndpoint),
	})
	ipAddresses := uniqueIPs([]net.IP{
		net.ParseIP(strings.TrimSpace(cfg.AgentIP)),
		parseEndpointIP(cfg.AgentGRPCEndpoint),
	})
	uris := buildAgentIdentityURIs(cfg)

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "agent-server:" + strings.TrimSpace(cfg.NodeID),
		},
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
		URIs:        uris,
	}, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create server csr failed: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	if len(keyPEM) == 0 || len(csrPEM) == 0 {
		return nil, nil, fmt.Errorf("encode server csr/key pem failed")
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
