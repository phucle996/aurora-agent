package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadManifest(rootDir string) (*ArtifactManifest, error) {
	manifestPath := filepath.Join(strings.TrimSpace(rootDir), "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest failed: %w", err)
	}

	var manifest ArtifactManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest failed: %w", err)
	}
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func validateManifest(manifest *ArtifactManifest) error {
	if manifest == nil {
		return fmt.Errorf("manifest is nil")
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		return fmt.Errorf("manifest schema_version is required")
	}
	manifest.SchemaVersion = normalizeManifestSchemaVersion(manifest.SchemaVersion)
	if manifest.SchemaVersion == "" {
		return fmt.Errorf("manifest schema_version is unsupported")
	}
	if strings.TrimSpace(manifest.Module) == "" {
		return fmt.Errorf("manifest module is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("manifest version is required")
	}
	if strings.TrimSpace(manifest.Runtime) == "" {
		manifest.Runtime = RuntimeLinuxSystemd
	}
	if strings.TrimSpace(manifest.Runtime) != RuntimeLinuxSystemd {
		return fmt.Errorf("manifest runtime is unsupported: %s", strings.TrimSpace(manifest.Runtime))
	}
	if strings.TrimSpace(manifest.Binary.Path) == "" {
		return fmt.Errorf("manifest binary.path is required")
	}
	if strings.TrimSpace(manifest.Binary.InstallPath) == "" {
		return fmt.Errorf("manifest binary.install_path is required")
	}
	for idx := range manifest.Assets {
		asset := &manifest.Assets[idx]
		if strings.TrimSpace(asset.Path) == "" {
			return fmt.Errorf("manifest assets[%d].path is required", idx)
		}
		if strings.TrimSpace(asset.InstallPath) == "" {
			return fmt.Errorf("manifest assets[%d].install_path is required", idx)
		}
	}
	if strings.TrimSpace(manifest.Service.Name) == "" {
		return fmt.Errorf("manifest service.name is required")
	}
	if strings.TrimSpace(manifest.Service.TemplatePath) == "" {
		return fmt.Errorf("manifest service.template_path is required")
	}
	if strings.TrimSpace(manifest.Service.UnitPath) == "" {
		manifest.Service.UnitPath = filepath.Join("/etc/systemd/system", strings.TrimSpace(manifest.Service.Name))
	}
	manifest.Capabilities = normalizeArtifactCapabilities(manifest.Capabilities, manifest)
	if !manifest.Capabilities.Install {
		return fmt.Errorf("manifest capability install must be enabled")
	}
	if manifest.Nginx.Enabled && !manifest.Capabilities.NginxIntegration {
		return fmt.Errorf("manifest capability nginx_integration must be enabled when nginx is configured")
	}
	if strings.TrimSpace(manifest.RuntimeBootstrap.ModuleName) != "" && !manifest.Capabilities.AdminRPCBootstrap {
		return fmt.Errorf("manifest capability admin_rpc_bootstrap must be enabled when runtime bootstrap is configured")
	}
	manifest.Healthcheck.Type = normalizeHealthcheckType(manifest.Healthcheck)
	switch manifest.Healthcheck.Type {
	case "systemd", "tcp", "http":
	default:
		return fmt.Errorf("manifest healthcheck.type is unsupported: %s", strings.TrimSpace(manifest.Healthcheck.Type))
	}
	if manifest.Healthcheck.Type == "http" && len(manifest.Healthcheck.Paths) == 0 {
		return fmt.Errorf("manifest healthcheck.paths is required when healthcheck.type is http")
	}
	if manifest.Nginx.Enabled {
		if strings.TrimSpace(manifest.Nginx.TemplatePath) == "" {
			return fmt.Errorf("manifest nginx.template_path is required when nginx is enabled")
		}
		if strings.TrimSpace(manifest.Nginx.SitePath) == "" {
			return fmt.Errorf("manifest nginx.site_path is required when nginx is enabled")
		}
	}
	return nil
}

func normalizeManifestSchemaVersion(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case strings.ToLower(BundleSchemaVersionV1), strings.ToLower(BundleSchemaVersionLegacy):
		return BundleSchemaVersionV1
	default:
		return ""
	}
}

func normalizeHealthcheckType(spec ArtifactHealthcheckSpec) string {
	switch strings.ToLower(strings.TrimSpace(spec.Type)) {
	case "http":
		return "http"
	case "tcp":
		return "tcp"
	case "systemd":
		return "systemd"
	case "":
		if len(spec.Paths) > 0 {
			return "http"
		}
		return "systemd"
	default:
		return strings.ToLower(strings.TrimSpace(spec.Type))
	}
}

func normalizeArtifactCapabilities(caps ArtifactCapabilities, manifest *ArtifactManifest) ArtifactCapabilities {
	if !caps.Install &&
		!caps.Restart &&
		!caps.Uninstall &&
		!caps.Migration &&
		!caps.AdminRPCBootstrap &&
		!caps.NginxIntegration {
		caps.Install = true
		caps.Restart = true
		caps.Uninstall = true
		if manifest != nil {
			caps.Migration = false
			caps.AdminRPCBootstrap = strings.TrimSpace(manifest.RuntimeBootstrap.ModuleName) != ""
			caps.NginxIntegration = manifest.Nginx.Enabled
		}
	}
	return caps
}
