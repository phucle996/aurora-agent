package adminrpc

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
)

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

	clientLeaf, err := loadLeafCertificate(c.cfg.AdminTLSClientCertPath)
	if err != nil {
		return err
	}
	serverLeaf, err := loadLeafCertificate(c.cfg.AgentTLSServerCertPath)
	if err != nil {
		return err
	}
	targetLeaf := clientLeaf
	if serverLeaf.NotAfter.Before(clientLeaf.NotAfter) {
		targetLeaf = serverLeaf
	}
	renewBefore := calculateRenewBeforeWindow(targetLeaf)
	if now.Before(targetLeaf.NotAfter.Add(-renewBefore)) {
		return nil
	}

	if c.logger != nil {
		c.logger.Info(
			"client certificate is nearing expiry; renewing via mTLS",
			"not_after", targetLeaf.NotAfter.UTC().Format(time.RFC3339),
			"renew_before", renewBefore.String(),
		)
	}
	return c.renewClientCertificate(ctx)
}

func (c *HeartbeatClient) renewClientCertificate(ctx context.Context) error {
	clientKeyPEM, clientCSRPEM, err := generateAgentClientKeyAndCSR(c.cfg)
	if err != nil {
		return err
	}
	serverKeyPEM, serverCSRPEM, err := generateAgentServerKeyAndCSR(c.cfg)
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
		"csr_pem":        strings.TrimSpace(string(clientCSRPEM)),
		"server_csr_pem": strings.TrimSpace(string(serverCSRPEM)),
		"hostname":       strings.TrimSpace(c.cfg.Hostname),
		"ip":             strings.TrimSpace(c.cfg.AgentIP),
		"cluster_id":     strings.TrimSpace(c.cfg.ClusterID),
	})
	if err != nil {
		return err
	}
	resp := &structpb.Struct{}
	if err := conn.Invoke(callCtx, runtimeRenewAgentCertPath, req, resp); err != nil {
		return err
	}

	clientCertPEM := strings.TrimSpace(readStructString(resp, "client_cert_pem"))
	serverCertPEM := strings.TrimSpace(readStructString(resp, "server_cert_pem"))
	adminServerCAPEM := strings.TrimSpace(readStructString(resp, "admin_server_ca_pem"))
	if clientCertPEM == "" || serverCertPEM == "" || adminServerCAPEM == "" {
		return fmt.Errorf("renew certificate response missing cert chain")
	}
	if err := writeSecureFile(c.cfg.AdminTLSClientKeyPath, []byte(strings.TrimSpace(string(clientKeyPEM))+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed client tls key failed: %w", err)
	}
	if err := writeSecureFile(c.cfg.AdminTLSClientCertPath, []byte(clientCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed client tls cert failed: %w", err)
	}
	if err := writeSecureFile(c.cfg.AgentTLSServerKeyPath, []byte(strings.TrimSpace(string(serverKeyPEM))+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed serving tls key failed: %w", err)
	}
	if err := writeSecureFile(c.cfg.AgentTLSServerCertPath, []byte(serverCertPEM+"\n"), 0o600); err != nil {
		return fmt.Errorf("write renewed serving tls cert failed: %w", err)
	}
	if err := writeSecureFile(c.cfg.AdminServerCAPath, []byte(adminServerCAPEM+"\n"), 0o600); err != nil {
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
