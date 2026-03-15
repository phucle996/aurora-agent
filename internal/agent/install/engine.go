package install

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = strings.TrimSpace(err.Error())
	}
	return fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), message)
}

type Engine struct {
	httpClient *http.Client
	runner     CommandRunner
}

func NewEngine() *Engine {
	return &Engine{
		httpClient: &http.Client{Timeout: 10 * time.Minute},
		runner:     execCommandRunner{},
	}
}

func (e *Engine) WithHTTPClient(client *http.Client) *Engine {
	if client != nil {
		e.httpClient = client
	}
	return e
}

func (e *Engine) WithCommandRunner(runner CommandRunner) *Engine {
	if runner != nil {
		e.runner = runner
	}
	return e
}

func (e *Engine) InstallModule(
	ctx context.Context,
	req InstallModuleRequest,
	logFn InstallLogFn,
) (result *InstallModuleResult, err error) {
	if e == nil {
		e = NewEngine()
	}
	if e.httpClient == nil {
		e.httpClient = &http.Client{Timeout: 10 * time.Minute}
	}
	if e.runner == nil {
		e.runner = execCommandRunner{}
	}

	var versionErr error
	req.APIVersion, versionErr = validateInstallerAPIVersion(req.APIVersion)
	if versionErr != nil {
		return nil, versionErr
	}
	if err := validateInstallRequest(req); err != nil {
		return nil, err
	}
	logInstallStage(logFn, InstallStageValidate, "validated install request")

	workDir, err := os.MkdirTemp("", "aurora-agent-install-*")
	if err != nil {
		return nil, fmt.Errorf("create installer workdir failed: %w", err)
	}
	defer os.RemoveAll(workDir)

	unpackDir := filepath.Join(workDir, "bundle")
	rollback := newInstallRollback(filepath.Join(workDir, "rollback"))
	var manifest *ArtifactManifest
	defer func() {
		if err == nil || manifest == nil {
			return
		}
		logInstallStage(logFn, InstallStageRollback, "rolling back partial install")
		if rollbackErr := rollback.Restore(ctx, e.runner, manifest, logFn); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("rollback failed: %w", rollbackErr))
		}
	}()
	if err := os.MkdirAll(unpackDir, 0o755); err != nil {
		return nil, fmt.Errorf("create unpack dir failed: %w", err)
	}

	logInstallStage(logFn, InstallStageDownload, "preparing artifact bundle")
	archivePath, cached, err := e.ensureArtifactAvailable(ctx, req.ArtifactURL, req.ArtifactChecksum)
	if err != nil {
		return nil, err
	}
	if cached {
		logInstallStage(logFn, InstallStageDownload, "using cached artifact bundle")
		logInstallStage(logFn, InstallStageVerify, "verified cached artifact checksum")
	} else {
		logInstallStage(logFn, InstallStageDownload, "downloaded artifact bundle")
		logInstallStage(logFn, InstallStageVerify, "verified downloaded artifact checksum")
	}

	logInstallStage(logFn, InstallStageUnpack, "unpacking artifact bundle")
	if err := unpackTarGz(archivePath, unpackDir); err != nil {
		return nil, err
	}

	manifest, err = loadManifest(unpackDir)
	if err != nil {
		return nil, err
	}
	if err := validateManifestAgainstRequest(manifest, req); err != nil {
		return nil, err
	}
	if err := validateInstallEnvAgainstManifest(manifest, req.Env); err != nil {
		return nil, err
	}

	data := templateData{
		Module:         strings.TrimSpace(req.Module),
		Version:        strings.TrimSpace(req.Version),
		AppHost:        strings.TrimSpace(req.AppHost),
		AppPort:        req.AppPort,
		BinaryPath:     strings.TrimSpace(manifest.Binary.InstallPath),
		EnvFilePath:    strings.TrimSpace(manifest.Env.Path),
		ServiceName:    strings.TrimSpace(manifest.Service.Name),
		NginxSitePath:  strings.TrimSpace(manifest.Nginx.SitePath),
		ArtifactURL:    strings.TrimSpace(req.ArtifactURL),
		ArtifactSHA256: strings.TrimSpace(req.ArtifactChecksum),
	}

	logInstallStage(logFn, InstallStageRender, "writing runtime files")
	if len(req.Files) > 0 {
		for path := range req.Files {
			if err := rollback.Capture(path); err != nil {
				return nil, err
			}
		}
		if err := writeInlineFiles(req.Files); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(manifest.Env.Path) != "" {
		if err := rollback.Capture(manifest.Env.Path); err != nil {
			return nil, err
		}
		if err := writeEnvFile(manifest.Env.Path, req.Env); err != nil {
			return nil, err
		}
	}
	if err := rollback.Capture(manifest.Binary.InstallPath); err != nil {
		return nil, err
	}
	if err := installBinary(filepath.Join(unpackDir, manifest.Binary.Path), manifest.Binary.InstallPath, manifest.Binary.Mode); err != nil {
		return nil, err
	}
	if err := rollback.Capture(manifest.Service.UnitPath); err != nil {
		return nil, err
	}
	if err := renderTemplateFile(
		filepath.Join(unpackDir, manifest.Service.TemplatePath),
		manifest.Service.UnitPath,
		data,
	); err != nil {
		return nil, err
	}
	if manifest.Nginx.Enabled {
		if err := rollback.Capture(manifest.Nginx.SitePath); err != nil {
			return nil, err
		}
		if err := renderTemplateFile(
			filepath.Join(unpackDir, manifest.Nginx.TemplatePath),
			manifest.Nginx.SitePath,
			data,
		); err != nil {
			return nil, err
		}
	}

	logInstallStage(logFn, InstallStageApply, "applying systemd/nginx changes")
	if err := e.runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return nil, err
	}
	if err := e.runner.Run(ctx, "systemctl", "enable", manifest.Service.Name); err != nil {
		return nil, err
	}
	if err := e.runner.Run(ctx, "systemctl", "restart", manifest.Service.Name); err != nil {
		return nil, err
	}
	if manifest.Nginx.Enabled {
		if err := e.runner.Run(ctx, "nginx", "-t"); err != nil {
			return nil, err
		}
		if err := e.runner.Run(ctx, "systemctl", "reload", "nginx"); err != nil {
			return nil, err
		}
	}

	logInstallStage(logFn, InstallStageHealth, "running healthcheck")
	health, err := runManifestHealthcheck(ctx, e.runner, manifest, req)
	if err != nil {
		return nil, err
	}

	logInstallStage(logFn, InstallStageCompleted, "module install completed")
	return &InstallModuleResult{
		APIVersion:            InstallerRPCVersionV1,
		Module:                strings.TrimSpace(req.Module),
		Version:               strings.TrimSpace(req.Version),
		Runtime:               RuntimeLinuxSystemd,
		ServiceName:           strings.TrimSpace(manifest.Service.Name),
		Endpoint:              buildModuleEndpoint(req),
		Status:                InstallStatusInstalled,
		Health:                health,
		ManifestSchemaVersion: manifest.SchemaVersion,
		Capabilities:          manifest.Capabilities,
	}, nil
}

func validateInstallRequest(req InstallModuleRequest) error {
	if strings.TrimSpace(req.Module) == "" {
		return fmt.Errorf("module is required")
	}
	if strings.TrimSpace(req.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if strings.EqualFold(strings.TrimSpace(req.Version), "latest") {
		return fmt.Errorf("version must be pinned to an exact release")
	}
	if strings.TrimSpace(req.ArtifactURL) == "" {
		return fmt.Errorf("artifact_url is required")
	}
	if strings.TrimSpace(req.ArtifactChecksum) == "" {
		return fmt.Errorf("artifact_checksum is required")
	}
	if strings.TrimSpace(req.AppHost) == "" {
		return fmt.Errorf("app_host is required")
	}
	if req.AppPort <= 0 || req.AppPort > 65535 {
		return fmt.Errorf("app_port is invalid")
	}
	return validateInstallPolicy(req)
}

func validateManifestAgainstRequest(manifest *ArtifactManifest, req InstallModuleRequest) error {
	if strings.TrimSpace(manifest.Module) != strings.TrimSpace(req.Module) {
		return fmt.Errorf("manifest module does not match request")
	}
	if strings.TrimSpace(manifest.Version) != strings.TrimSpace(req.Version) {
		return fmt.Errorf("manifest version does not match request")
	}
	return nil
}

func validateInstallEnvAgainstManifest(manifest *ArtifactManifest, env map[string]string) error {
	if manifest == nil {
		return fmt.Errorf("manifest is nil")
	}
	if !manifest.Capabilities.AdminRPCBootstrap {
		return nil
	}
	if strings.TrimSpace(env["ADMIN_RPC_ENDPOINT"]) == "" {
		return fmt.Errorf("manifest requires ADMIN_RPC_ENDPOINT")
	}
	if strings.TrimSpace(env["ADMIN_RPC_BOOTSTRAP_TOKEN"]) == "" {
		return fmt.Errorf("manifest requires ADMIN_RPC_BOOTSTRAP_TOKEN")
	}
	return nil
}

func installBinary(sourcePath string, installPath string, modeRaw string) error {
	source := strings.TrimSpace(sourcePath)
	target := strings.TrimSpace(installPath)
	if source == "" || target == "" {
		return fmt.Errorf("binary source/install path is empty")
	}
	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create binary install dir failed: %w", err)
	}
	src, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open binary source failed: %w", err)
	}
	defer src.Close()
	mode := parseFileMode(modeRaw, 0o755)
	tmp, err := os.CreateTemp(targetDir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create installed binary temp file failed: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod installed binary temp file failed: %w", err)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copy installed binary failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close installed binary failed: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace installed binary failed: %w", err)
	}
	return nil
}

func parseFileMode(raw string, fallback os.FileMode) os.FileMode {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 8, 32)
	if err != nil {
		return fallback
	}
	return os.FileMode(parsed)
}

func writeInlineFiles(files map[string]string) error {
	for path, content := range files {
		cleanPath := strings.TrimSpace(path)
		if cleanPath == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
			return fmt.Errorf("create inline file dir failed: %w", err)
		}
		if err := os.WriteFile(cleanPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write inline file failed: %w", err)
		}
	}
	return nil
}

func runManifestHealthcheck(ctx context.Context, runner CommandRunner, manifest *ArtifactManifest, req InstallModuleRequest) (string, error) {
	if manifest == nil {
		return ModuleHealthUnhealthy, fmt.Errorf("manifest is nil")
	}
	timeout := 8 * time.Second
	if manifest.Healthcheck.TimeoutSeconds > 0 {
		timeout = time.Duration(manifest.Healthcheck.TimeoutSeconds) * time.Second
	}
	switch normalizeHealthcheckType(manifest.Healthcheck) {
	case "systemd":
		if err := checkSystemdServiceActive(ctx, runner, manifest.Service.Name); err != nil {
			return ModuleHealthUnhealthy, err
		}
		return ModuleHealthHealthy, nil
	case "tcp":
		addressHost := strings.TrimSpace(manifest.Healthcheck.Host)
		if addressHost == "" {
			addressHost = "127.0.0.1"
		}
		address := net.JoinHostPort(addressHost, strconv.Itoa(int(req.AppPort)))
		deadline := time.Now().Add(timeout)
		var lastErr error
		for time.Now().Before(deadline) {
			dialer := &net.Dialer{Timeout: minDuration(2*time.Second, timeout)}
			conn, err := dialer.DialContext(ctx, "tcp", address)
			if err == nil {
				_ = conn.Close()
				return ModuleHealthHealthy, nil
			}
			lastErr = err
			time.Sleep(500 * time.Millisecond)
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("dial timeout")
		}
		return ModuleHealthUnhealthy, fmt.Errorf("module tcp healthcheck failed: %w", lastErr)
	}

	scheme := strings.TrimSpace(manifest.Healthcheck.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	host := strings.TrimSpace(manifest.Healthcheck.Host)
	if host == "" {
		host = "127.0.0.1"
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	baseURL := fmt.Sprintf("%s://%s:%d", scheme, host, req.AppPort)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := checkSystemdServiceActive(ctx, runner, manifest.Service.Name); err != nil {
			lastErr = fmt.Errorf("service is not active: %w", err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		for _, path := range manifest.Healthcheck.Paths {
			target := strings.TrimSpace(path)
			if target == "" {
				continue
			}
			reqCtx, cancel := context.WithTimeout(ctx, minDuration(3*time.Second, timeout))
			httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+target, nil)
			if err != nil {
				cancel()
				lastErr = err
				continue
			}
			resp, err := client.Do(httpReq)
			cancel()
			if err == nil && resp != nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 500 {
					return ModuleHealthHealthy, nil
				}
				lastErr = fmt.Errorf("unexpected status %d from %s", resp.StatusCode, target)
				continue
			}
			lastErr = fmt.Errorf("request %s failed: %w", target, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout after %s", timeout)
	}
	return ModuleHealthUnhealthy, fmt.Errorf("module healthcheck failed: %w", lastErr)
}

func checkSystemdServiceActive(ctx context.Context, runner CommandRunner, serviceName string) error {
	if runner == nil {
		runner = execCommandRunner{}
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return fmt.Errorf("service name is required for healthcheck")
	}
	return runner.Run(ctx, "systemctl", "is-active", "--quiet", serviceName)
}

func buildModuleEndpoint(req InstallModuleRequest) string {
	host := strings.TrimSpace(req.AppHost)
	if host == "" || req.AppPort <= 0 {
		return ""
	}
	return fmt.Sprintf("https://%s", host)
}

func logInstallStage(logFn InstallLogFn, stage InstallStage, message string) {
	if logFn != nil {
		logFn(stage, strings.TrimSpace(message))
	}
}

func minDuration(a time.Duration, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func normalizeInstallerAPIVersion(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", InstallerRPCVersionV1:
		return InstallerRPCVersionV1
	default:
		return ""
	}
}

func validateInstallerAPIVersion(raw string) (string, error) {
	normalized := normalizeInstallerAPIVersion(raw)
	if normalized == "" {
		return "", fmt.Errorf("api_version is unsupported")
	}
	return normalized, nil
}
