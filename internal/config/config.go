package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	HardcodedVersion string = "V0.3"
)

type Config struct {
	NodeID            string
	Hostname          string
	ProbeListenAddr   string
	HealthInterval    time.Duration
	ShutdownTimeout   time.Duration
	AdminGRPCAddr     string
	AdminServerName   string
	AdminClientCN     string
	AdminTLSCAPath    string
	AdminTLSCertPath  string
	AdminTLSKeyPath   string
	BootstrapToken    string
	ClusterID         string
	AgentIP           string
	HeartbeatInterval time.Duration
	AgentGRPCEndpoint string
	Platform          string
	AgentVersion      string
	LogLevel          string
}

func Load() (Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}

	cfg := Config{
		NodeID:            env("AURORA_NODE_ID", hostname),
		Hostname:          hostname,
		ProbeListenAddr:   env("AURORA_AGENT_PROBE_ADDR", "0.0.0.0:7443"),
		HealthInterval:    envDuration("AURORA_HEALTH_INTERVAL", 10*time.Second),
		ShutdownTimeout:   envDuration("AURORA_SHUTDOWN_TIMEOUT", 20*time.Second),
		AdminGRPCAddr:     env("AURORA_ADMIN_GRPC_ADDR", ""),
		AdminServerName:   env("AURORA_ADMIN_SERVER_NAME", ""),
		AdminClientCN:     env("AURORA_AGENT_ADMIN_CLIENT_CN", env("AURORA_ADMIN_SERVER_NAME", "admin.aurora.local")),
		AdminTLSCAPath:    env("AURORA_ADMIN_TLS_CA_PATH", ""),
		AdminTLSCertPath:  env("AURORA_ADMIN_TLS_CERT_PATH", ""),
		AdminTLSKeyPath:   env("AURORA_ADMIN_TLS_KEY_PATH", ""),
		BootstrapToken:    env("AURORA_AGENT_BOOTSTRAP_TOKEN", ""),
		ClusterID:         env("AURORA_AGENT_CLUSTER_ID", ""),
		AgentIP:           env("AURORA_AGENT_IP", ""),
		HeartbeatInterval: envDuration("AURORA_AGENT_HEARTBEAT_INTERVAL", 15*time.Second),
		AgentGRPCEndpoint: env("AURORA_AGENT_GRPC_ENDPOINT", ""),
		Platform:          env("AURORA_AGENT_PLATFORM", "linux"),
		AgentVersion:      HardcodedVersion,
		LogLevel:          strings.ToLower(env("AURORA_LOG_LEVEL", "info")),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.NodeID == "" {
		return errors.New("AURORA_NODE_ID is required")
	}
	if strings.TrimSpace(c.AgentVersion) == "" {
		return errors.New("agent version must not be empty")
	}
	if strings.TrimSpace(c.ProbeListenAddr) == "" {
		return errors.New("AURORA_AGENT_PROBE_ADDR is required")
	}

	if c.ShutdownTimeout <= 0 {
		return errors.New("AURORA_SHUTDOWN_TIMEOUT must be > 0")
	}
	if strings.TrimSpace(c.AdminGRPCAddr) == "" {
		return errors.New("AURORA_ADMIN_GRPC_ADDR is required")
	}
	if strings.TrimSpace(c.AdminTLSCAPath) == "" {
		return errors.New("AURORA_ADMIN_TLS_CA_PATH is required")
	}
	if strings.TrimSpace(c.AdminTLSCertPath) == "" || strings.TrimSpace(c.AdminTLSKeyPath) == "" {
		return errors.New("AURORA_ADMIN_TLS_CERT_PATH and AURORA_ADMIN_TLS_KEY_PATH are required")
	}
	hasCertFiles := fileExists(strings.TrimSpace(c.AdminTLSCertPath)) && fileExists(strings.TrimSpace(c.AdminTLSKeyPath))
	if !hasCertFiles && strings.TrimSpace(c.BootstrapToken) == "" {
		return errors.New("mTLS cert/key files are missing and AURORA_AGENT_BOOTSTRAP_TOKEN is empty")
	}
	if c.HeartbeatInterval <= 0 {
		return errors.New("AURORA_AGENT_HEARTBEAT_INTERVAL must be > 0")
	}

	return nil
}

func (c Config) AdminTLSConfig() (*tls.Config, error) {
	caBytes, err := os.ReadFile(strings.TrimSpace(c.AdminTLSCAPath))
	if err != nil {
		return nil, fmt.Errorf("read admin CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("append admin CA cert failed")
	}
	crt, err := tls.LoadX509KeyPair(strings.TrimSpace(c.AdminTLSCertPath), strings.TrimSpace(c.AdminTLSKeyPath))
	if err != nil {
		return nil, fmt.Errorf("load admin mTLS cert/key: %w", err)
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
		Certificates: []tls.Certificate{crt},
	}
	if serverName := strings.TrimSpace(c.AdminServerName); serverName != "" {
		tlsCfg.ServerName = serverName
	}
	return tlsCfg, nil
}

func (c Config) ProbeServerTLSConfig() (*tls.Config, error) {
	caBytes, err := os.ReadFile(strings.TrimSpace(c.AdminTLSCAPath))
	if err != nil {
		return nil, fmt.Errorf("read admin CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("append admin CA cert failed")
	}
	crt, err := tls.LoadX509KeyPair(strings.TrimSpace(c.AdminTLSCertPath), strings.TrimSpace(c.AdminTLSKeyPath))
	if err != nil {
		return nil, fmt.Errorf("load agent probe cert/key: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		Certificates: []tls.Certificate{crt},
	}, nil
}

func env(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
