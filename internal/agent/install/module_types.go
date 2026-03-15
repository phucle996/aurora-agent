package install

type InstallStage string

const (
	InstallStageValidate  InstallStage = "validate"
	InstallStageDownload  InstallStage = "download"
	InstallStageVerify    InstallStage = "verify"
	InstallStageUnpack    InstallStage = "unpack"
	InstallStageRender    InstallStage = "render"
	InstallStageApply     InstallStage = "apply"
	InstallStageHealth    InstallStage = "healthcheck"
	InstallStageRollback  InstallStage = "rollback"
	InstallStageCompleted InstallStage = "completed"
)

const (
	RuntimeLinuxSystemd = "linux-systemd"
)

const (
	InstallerRPCVersionV1     = "aurora.installer.rpc.v1"
	BundleSchemaVersionV1     = "aurora.installer.bundle.v1"
	BundleSchemaVersionLegacy = "v1"
)

const (
	InstallStatusUnknown    = "unknown"
	InstallStatusInstalling = "installing"
	InstallStatusInstalled  = "installed"
	InstallStatusFailed     = "failed"
)

const (
	ModuleHealthUnknown   = "unknown"
	ModuleHealthStarting  = "starting"
	ModuleHealthHealthy   = "healthy"
	ModuleHealthUnhealthy = "unhealthy"
	ModuleHealthDegraded  = "degraded"
)

type InstallLogFn func(stage InstallStage, message string)

type InstallLogEntry struct {
	Stage   InstallStage `json:"stage"`
	Message string       `json:"message"`
}

type InstallModuleRequest struct {
	APIVersion       string            `json:"api_version,omitempty"`
	RequestID        string            `json:"request_id"`
	Module           string            `json:"module"`
	Version          string            `json:"version"`
	ArtifactURL      string            `json:"artifact_url"`
	ArtifactChecksum string            `json:"artifact_checksum"`
	AppHost          string            `json:"app_host"`
	AppPort          int32             `json:"app_port"`
	Env              map[string]string `json:"env,omitempty"`
	Files            map[string]string `json:"files,omitempty"`
}

type InstallModuleResult struct {
	APIVersion            string               `json:"api_version,omitempty"`
	Module                string               `json:"module"`
	Version               string               `json:"version"`
	Runtime               string               `json:"runtime"`
	ServiceName           string               `json:"service_name,omitempty"`
	UnitPath              string               `json:"unit_path,omitempty"`
	BinaryPath            string               `json:"binary_path,omitempty"`
	EnvFilePath           string               `json:"env_file_path,omitempty"`
	NginxSitePath         string               `json:"nginx_site_path,omitempty"`
	AssetPaths            []string             `json:"asset_paths,omitempty"`
	Endpoint              string               `json:"endpoint,omitempty"`
	Status                string               `json:"status"`
	Health                string               `json:"health,omitempty"`
	ManifestSchemaVersion string               `json:"manifest_schema_version,omitempty"`
	Capabilities          ArtifactCapabilities `json:"capabilities,omitempty"`
}

type InstallModuleResponse struct {
	APIVersion string               `json:"api_version,omitempty"`
	OK         bool                 `json:"ok"`
	Result     *InstallModuleResult `json:"result,omitempty"`
	Logs       []InstallLogEntry    `json:"logs,omitempty"`
	ErrorText  string               `json:"error_text,omitempty"`
}

type InstallModuleStreamEvent struct {
	APIVersion string               `json:"api_version,omitempty"`
	Type       string               `json:"type"`
	Stage      InstallStage         `json:"stage,omitempty"`
	Message    string               `json:"message,omitempty"`
	Result     *InstallModuleResult `json:"result,omitempty"`
	ErrorText  string               `json:"error_text,omitempty"`
}

type RestartModuleRequest struct {
	APIVersion  string `json:"api_version,omitempty"`
	RequestID   string `json:"request_id"`
	Module      string `json:"module"`
	ServiceName string `json:"service_name"`
}

type RestartModuleResult struct {
	APIVersion  string `json:"api_version,omitempty"`
	Module      string `json:"module"`
	Runtime     string `json:"runtime"`
	ServiceName string `json:"service_name"`
	Status      string `json:"status"`
	Health      string `json:"health,omitempty"`
}

type RestartModuleResponse struct {
	APIVersion string               `json:"api_version,omitempty"`
	OK         bool                 `json:"ok"`
	Result     *RestartModuleResult `json:"result,omitempty"`
	Logs       []InstallLogEntry    `json:"logs,omitempty"`
	ErrorText  string               `json:"error_text,omitempty"`
}

type UninstallModuleRequest struct {
	APIVersion    string   `json:"api_version,omitempty"`
	RequestID     string   `json:"request_id"`
	Module        string   `json:"module"`
	ServiceName   string   `json:"service_name"`
	UnitPath      string   `json:"unit_path,omitempty"`
	BinaryPath    string   `json:"binary_path,omitempty"`
	EnvFilePath   string   `json:"env_file_path,omitempty"`
	NginxSitePath string   `json:"nginx_site_path,omitempty"`
	AssetPaths    []string `json:"asset_paths,omitempty"`
}

type UninstallModuleResult struct {
	APIVersion  string `json:"api_version,omitempty"`
	Module      string `json:"module"`
	Runtime     string `json:"runtime"`
	ServiceName string `json:"service_name"`
	Status      string `json:"status"`
	Health      string `json:"health,omitempty"`
}

type UninstallModuleResponse struct {
	APIVersion string                 `json:"api_version,omitempty"`
	OK         bool                   `json:"ok"`
	Result     *UninstallModuleResult `json:"result,omitempty"`
	Logs       []InstallLogEntry      `json:"logs,omitempty"`
	ErrorText  string                 `json:"error_text,omitempty"`
}

type InstalledModuleRecord struct {
	APIVersion            string               `json:"api_version,omitempty"`
	Module                string               `json:"module"`
	Version               string               `json:"version"`
	Runtime               string               `json:"runtime"`
	ServiceName           string               `json:"service_name,omitempty"`
	UnitPath              string               `json:"unit_path,omitempty"`
	BinaryPath            string               `json:"binary_path,omitempty"`
	EnvFilePath           string               `json:"env_file_path,omitempty"`
	NginxSitePath         string               `json:"nginx_site_path,omitempty"`
	AssetPaths            []string             `json:"asset_paths,omitempty"`
	Endpoint              string               `json:"endpoint,omitempty"`
	Status                string               `json:"status"`
	Health                string               `json:"health,omitempty"`
	ObservedAt            string               `json:"observed_at"`
	ManifestSchemaVersion string               `json:"manifest_schema_version,omitempty"`
	Capabilities          ArtifactCapabilities `json:"capabilities,omitempty"`
}

type ListInstalledModulesRequest struct {
	APIVersion string `json:"api_version,omitempty"`
}

type ListInstalledModulesResponse struct {
	APIVersion string                  `json:"api_version,omitempty"`
	OK         bool                    `json:"ok"`
	Items      []InstalledModuleRecord `json:"items,omitempty"`
	ErrorText  string                  `json:"error_text,omitempty"`
}

type ArtifactManifest struct {
	SchemaVersion    string                   `json:"schema_version"`
	Module           string                   `json:"module"`
	Version          string                   `json:"version"`
	Runtime          string                   `json:"runtime"`
	Capabilities     ArtifactCapabilities     `json:"capabilities"`
	Binary           ArtifactBinarySpec       `json:"binary"`
	Assets           []ArtifactAssetSpec      `json:"assets,omitempty"`
	Service          ArtifactServiceSpec      `json:"service"`
	Nginx            ArtifactNginxSpec        `json:"nginx"`
	Env              ArtifactEnvSpec          `json:"env"`
	RuntimeBootstrap ArtifactRuntimeBootstrap `json:"runtime_bootstrap"`
	Healthcheck      ArtifactHealthcheckSpec  `json:"healthcheck"`
}

type ArtifactCapabilities struct {
	Install           bool `json:"install"`
	Restart           bool `json:"restart"`
	Uninstall         bool `json:"uninstall"`
	Migration         bool `json:"migration"`
	AdminRPCBootstrap bool `json:"admin_rpc_bootstrap"`
	NginxIntegration  bool `json:"nginx_integration"`
}

type ArtifactBinarySpec struct {
	Path        string `json:"path"`
	InstallPath string `json:"install_path"`
	Mode        string `json:"mode"`
}

type ArtifactAssetSpec struct {
	Path        string `json:"path"`
	InstallPath string `json:"install_path"`
}

type ArtifactServiceSpec struct {
	Name         string `json:"name"`
	TemplatePath string `json:"template_path"`
	UnitPath     string `json:"unit_path"`
}

type ArtifactNginxSpec struct {
	Enabled      bool   `json:"enabled"`
	TemplatePath string `json:"template_path"`
	SitePath     string `json:"site_path"`
}

type ArtifactEnvSpec struct {
	Path string `json:"path"`
}

type ArtifactRuntimeBootstrap struct {
	ModuleName string `json:"module_name"`
}

type ArtifactHealthcheckSpec struct {
	Type           string   `json:"type"`
	Scheme         string   `json:"scheme"`
	Host           string   `json:"host,omitempty"`
	Paths          []string `json:"paths"`
	TimeoutSeconds int32    `json:"timeout_seconds"`
}

type templateData struct {
	Module         string
	Version        string
	AppHost        string
	AppPort        int32
	BinaryPath     string
	EnvFilePath    string
	ServiceName    string
	NginxSitePath  string
	ArtifactURL    string
	ArtifactSHA256 string
}
