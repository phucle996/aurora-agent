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
	NodeID                 string
	ServiceID              string
	Role                   string
	Hostname               string
	ProbeListenAddr        string
	HealthInterval         time.Duration
	ShutdownTimeout        time.Duration
	AdminGRPCAddr          string
	AdminServerName        string
	AdminClientCN          string
	AdminClientServiceID   string
	AdminClientRole        string
	AdminServerCAPath      string
	AdminClientCAPath      string
	AdminTLSClientCertPath string
	AdminTLSClientKeyPath  string
	AgentTLSServerCertPath string
	AgentTLSServerKeyPath  string
	BootstrapToken         string
	ClusterID              string
	AgentIP                string
	HeartbeatInterval      time.Duration
	AgentGRPCEndpoint      string
	Platform               string
	AgentVersion           string
	LogLevel               string
}

func Load() (Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}

	legacyCAPath := env("AURORA_ADMIN_TLS_CA_PATH", "")
	cfg := Config{
		NodeID:                 env("AURORA_NODE_ID", hostname),
		ServiceID:              env("AURORA_AGENT_SERVICE_ID", "aurora-agent"),
		Role:                   env("AURORA_AGENT_ROLE", "agent"),
		Hostname:               hostname,
		ProbeListenAddr:        env("AURORA_AGENT_PROBE_ADDR", "0.0.0.0:7443"),
		HealthInterval:         envDuration("AURORA_HEALTH_INTERVAL", 10*time.Second),
		ShutdownTimeout:        envDuration("AURORA_SHUTDOWN_TIMEOUT", 20*time.Second),
		AdminGRPCAddr:          env("AURORA_ADMIN_GRPC_ADDR", ""),
		AdminServerName:        env("AURORA_ADMIN_SERVER_NAME", ""),
		AdminClientCN:          env("AURORA_AGENT_ADMIN_CLIENT_CN", env("AURORA_ADMIN_SERVER_NAME", "admin.aurora.local")),
		AdminClientServiceID:   env("AURORA_AGENT_ADMIN_CLIENT_SERVICE_ID", "aurora-admin"),
		AdminClientRole:        env("AURORA_AGENT_ADMIN_CLIENT_ROLE", "control-plane"),
		AdminServerCAPath:      env("AURORA_ADMIN_SERVER_CA_PATH", legacyCAPath),
		AdminClientCAPath:      env("AURORA_ADMIN_CLIENT_CA_PATH", env("AURORA_ADMIN_SERVER_CA_PATH", legacyCAPath)),
		AdminTLSClientCertPath: env("AURORA_ADMIN_TLS_CLIENT_CERT_PATH", env("AURORA_ADMIN_TLS_CERT_PATH", "")),
		AdminTLSClientKeyPath:  env("AURORA_ADMIN_TLS_CLIENT_KEY_PATH", env("AURORA_ADMIN_TLS_KEY_PATH", "")),
		AgentTLSServerCertPath: env("AURORA_AGENT_TLS_SERVER_CERT_PATH", env("AURORA_ADMIN_TLS_CERT_PATH", "")),
		AgentTLSServerKeyPath:  env("AURORA_AGENT_TLS_SERVER_KEY_PATH", env("AURORA_ADMIN_TLS_KEY_PATH", "")),
		BootstrapToken:         env("AURORA_AGENT_BOOTSTRAP_TOKEN", ""),
		ClusterID:              env("AURORA_AGENT_CLUSTER_ID", ""),
		AgentIP:                env("AURORA_AGENT_IP", ""),
		HeartbeatInterval:      envDuration("AURORA_AGENT_HEARTBEAT_INTERVAL", 15*time.Second),
		AgentGRPCEndpoint:      env("AURORA_AGENT_GRPC_ENDPOINT", ""),
		Platform:               env("AURORA_AGENT_PLATFORM", "linux"),
		AgentVersion:           HardcodedVersion,
		LogLevel:               strings.ToLower(env("AURORA_LOG_LEVEL", "info")),
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
	if strings.TrimSpace(c.ServiceID) == "" {
		return errors.New("AURORA_AGENT_SERVICE_ID is required")
	}
	if strings.TrimSpace(c.Role) == "" {
		return errors.New("AURORA_AGENT_ROLE is required")
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
	if strings.TrimSpace(c.AdminServerCAPath) == "" {
		return errors.New("AURORA_ADMIN_SERVER_CA_PATH is required")
	}
	if strings.TrimSpace(c.AdminClientCAPath) == "" {
		return errors.New("AURORA_ADMIN_CLIENT_CA_PATH is required")
	}
	if strings.TrimSpace(c.AdminTLSClientCertPath) == "" || strings.TrimSpace(c.AdminTLSClientKeyPath) == "" {
		return errors.New("AURORA_ADMIN_TLS_CLIENT_CERT_PATH and AURORA_ADMIN_TLS_CLIENT_KEY_PATH are required")
	}
	if strings.TrimSpace(c.AgentTLSServerCertPath) == "" || strings.TrimSpace(c.AgentTLSServerKeyPath) == "" {
		return errors.New("AURORA_AGENT_TLS_SERVER_CERT_PATH and AURORA_AGENT_TLS_SERVER_KEY_PATH are required")
	}
	hasClientFiles := fileExists(strings.TrimSpace(c.AdminTLSClientCertPath)) && fileExists(strings.TrimSpace(c.AdminTLSClientKeyPath))
	hasServerFiles := fileExists(strings.TrimSpace(c.AgentTLSServerCertPath)) && fileExists(strings.TrimSpace(c.AgentTLSServerKeyPath))
	if (!hasClientFiles || !hasServerFiles) && strings.TrimSpace(c.BootstrapToken) == "" {
		return errors.New("mTLS cert/key files are missing and AURORA_AGENT_BOOTSTRAP_TOKEN is empty")
	}
	if c.HeartbeatInterval <= 0 {
		return errors.New("AURORA_AGENT_HEARTBEAT_INTERVAL must be > 0")
	}

	return nil
}

func (c Config) AdminTLSConfig() (*tls.Config, error) {
	caBytes, err := os.ReadFile(strings.TrimSpace(c.AdminServerCAPath))
	if err != nil {
		return nil, fmt.Errorf("read admin server CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("append admin CA cert failed")
	}
	crt, err := tls.LoadX509KeyPair(strings.TrimSpace(c.AdminTLSClientCertPath), strings.TrimSpace(c.AdminTLSClientKeyPath))
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
	caBytes, err := os.ReadFile(strings.TrimSpace(c.AdminClientCAPath))
	if err != nil {
		return nil, fmt.Errorf("read admin client CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("append admin CA cert failed")
	}
	crt, err := tls.LoadX509KeyPair(strings.TrimSpace(c.AgentTLSServerCertPath), strings.TrimSpace(c.AgentTLSServerKeyPath))
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
