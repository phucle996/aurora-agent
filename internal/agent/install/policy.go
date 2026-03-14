package install

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
)

type ArtifactSourcePolicy struct {
	RepoSlug        string
	BundleAssetBase string
}

type Policy struct {
	AllowedModules   map[string]struct{}
	AllowedHosts     map[string]struct{}
	AllowedRepoSlugs map[string]struct{}
	AllowedArtifacts map[string]ArtifactSourcePolicy
	AuditLogPath     string
}

var (
	policyMu      sync.RWMutex
	installPolicy = defaultPolicy()
)

func ConfigurePolicy(policy Policy) {
	policyMu.Lock()
	defer policyMu.Unlock()
	installPolicy = normalizePolicy(policy)
}

func currentPolicy() Policy {
	policyMu.RLock()
	defer policyMu.RUnlock()
	return clonePolicy(installPolicy)
}

func defaultPolicy() Policy {
	return normalizePolicy(Policy{
		AllowedModules: map[string]struct{}{
			"ums":      {},
			"platform": {},
			"paas":     {},
			"dbaas":    {},
		},
		AllowedHosts: map[string]struct{}{
			"github.com":                           {},
			"release-assets.githubusercontent.com": {},
			"objects.githubusercontent.com":        {},
		},
		AllowedArtifacts: defaultArtifactSourcePolicies(),
		AuditLogPath:     "/var/lib/aurora-agent/install_audit.jsonl",
	})
}

func normalizePolicy(policy Policy) Policy {
	normalized := Policy{
		AllowedModules:   make(map[string]struct{}, len(policy.AllowedModules)),
		AllowedHosts:     make(map[string]struct{}, len(policy.AllowedHosts)),
		AllowedRepoSlugs: make(map[string]struct{}, len(policy.AllowedRepoSlugs)),
		AllowedArtifacts: make(map[string]ArtifactSourcePolicy, len(policy.AllowedArtifacts)),
		AuditLogPath:     strings.TrimSpace(policy.AuditLogPath),
	}
	for module := range policy.AllowedModules {
		value := normalizeModuleName(module)
		if value == "" {
			continue
		}
		normalized.AllowedModules[value] = struct{}{}
	}
	for host := range policy.AllowedHosts {
		value := normalizeArtifactHost(host)
		if value == "" {
			continue
		}
		normalized.AllowedHosts[value] = struct{}{}
	}
	for repo := range policy.AllowedRepoSlugs {
		value := normalizeRepoSlug(repo)
		if value == "" {
			continue
		}
		normalized.AllowedRepoSlugs[value] = struct{}{}
	}
	for module, source := range policy.AllowedArtifacts {
		moduleName := normalizeModuleName(module)
		repoSlug := normalizeRepoSlug(source.RepoSlug)
		assetBase := strings.TrimSpace(source.BundleAssetBase)
		if moduleName == "" || repoSlug == "" || assetBase == "" {
			continue
		}
		normalized.AllowedArtifacts[moduleName] = ArtifactSourcePolicy{
			RepoSlug:        repoSlug,
			BundleAssetBase: assetBase,
		}
		normalized.AllowedRepoSlugs[repoSlug] = struct{}{}
	}
	if len(normalized.AllowedModules) == 0 {
		normalized.AllowedModules = defaultPolicy().AllowedModules
	}
	if len(normalized.AllowedHosts) == 0 {
		normalized.AllowedHosts = defaultPolicy().AllowedHosts
	}
	if len(normalized.AllowedArtifacts) == 0 {
		normalized.AllowedArtifacts = cloneArtifactPolicies(defaultPolicy().AllowedArtifacts)
	}
	if len(normalized.AllowedRepoSlugs) == 0 {
		for repo := range defaultPolicy().AllowedRepoSlugs {
			normalized.AllowedRepoSlugs[repo] = struct{}{}
		}
	}
	if normalized.AuditLogPath == "" {
		normalized.AuditLogPath = defaultPolicy().AuditLogPath
	}
	return normalized
}

func clonePolicy(policy Policy) Policy {
	cloned := Policy{
		AllowedModules:   make(map[string]struct{}, len(policy.AllowedModules)),
		AllowedHosts:     make(map[string]struct{}, len(policy.AllowedHosts)),
		AllowedRepoSlugs: make(map[string]struct{}, len(policy.AllowedRepoSlugs)),
		AllowedArtifacts: make(map[string]ArtifactSourcePolicy, len(policy.AllowedArtifacts)),
		AuditLogPath:     strings.TrimSpace(policy.AuditLogPath),
	}
	for module := range policy.AllowedModules {
		cloned.AllowedModules[module] = struct{}{}
	}
	for host := range policy.AllowedHosts {
		cloned.AllowedHosts[host] = struct{}{}
	}
	for repo := range policy.AllowedRepoSlugs {
		cloned.AllowedRepoSlugs[repo] = struct{}{}
	}
	for module, source := range policy.AllowedArtifacts {
		cloned.AllowedArtifacts[module] = source
	}
	return cloned
}

func cloneArtifactPolicies(in map[string]ArtifactSourcePolicy) map[string]ArtifactSourcePolicy {
	out := make(map[string]ArtifactSourcePolicy, len(in))
	for module, source := range in {
		out[module] = source
	}
	return out
}

func normalizeModuleName(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	return strings.Trim(value, "-/")
}

func normalizeArtifactHost(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.Trim(value, "/")
	return value
}

func normalizeRepoSlug(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.Trim(value, "/")
}

func validateInstallPolicy(req InstallModuleRequest) error {
	policy := currentPolicy()
	moduleName := normalizeModuleName(req.Module)
	if _, ok := policy.AllowedModules[moduleName]; !ok {
		return fmt.Errorf("module %s is not allowed by agent install policy", moduleName)
	}
	if err := validateArtifactURL(req.ArtifactURL, policy.AllowedHosts); err != nil {
		return err
	}
	if err := validateArtifactSourceForModule(req.ArtifactURL, moduleName, policy); err != nil {
		return err
	}
	return nil
}

func validateArtifactURL(raw string, allowedHosts map[string]struct{}) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("artifact_url is invalid")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("artifact_url must use https")
	}
	host := normalizeArtifactHost(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("artifact_url host is empty")
	}
	if _, ok := allowedHosts[host]; !ok {
		return fmt.Errorf("artifact host %s is not allowed by agent install policy", host)
	}
	return nil
}

func validateArtifactSourceForModule(raw string, moduleName string, policy Policy) error {
	source, ok := policy.AllowedArtifacts[moduleName]
	if !ok {
		return fmt.Errorf("module %s does not have an allowed artifact source", moduleName)
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("artifact_url is invalid")
	}
	if normalizeArtifactHost(parsed.Hostname()) != "github.com" {
		return fmt.Errorf("artifact_url must use a github.com release path")
	}
	repoSlug, assetName, err := parseGitHubReleaseAsset(parsed.Path)
	if err != nil {
		return err
	}
	if repoSlug != normalizeRepoSlug(source.RepoSlug) {
		return fmt.Errorf("artifact repo %s is not allowed for module %s", repoSlug, moduleName)
	}
	if _, ok := policy.AllowedRepoSlugs[repoSlug]; !ok {
		return fmt.Errorf("artifact repo %s is not allowed by agent install policy", repoSlug)
	}
	prefix := strings.TrimSpace(source.BundleAssetBase) + "_linux_"
	if !strings.HasPrefix(assetName, prefix) || !strings.HasSuffix(assetName, "_bundle.tar.gz") {
		return fmt.Errorf("artifact asset %s is not allowed for module %s", assetName, moduleName)
	}
	return nil
}

func parseGitHubReleaseAsset(rawPath string) (string, string, error) {
	clean := path.Clean(strings.TrimSpace(rawPath))
	parts := strings.Split(strings.Trim(clean, "/"), "/")
	if len(parts) < 5 {
		return "", "", fmt.Errorf("artifact_url must target a github release asset")
	}
	if parts[2] != "releases" || parts[3] != "download" {
		return "", "", fmt.Errorf("artifact_url must target a github release asset")
	}
	repoSlug := normalizeRepoSlug(parts[0] + "/" + parts[1])
	assetName := strings.TrimSpace(parts[len(parts)-1])
	if repoSlug == "" || assetName == "" {
		return "", "", fmt.Errorf("artifact_url must target a github release asset")
	}
	return repoSlug, assetName, nil
}

func defaultArtifactSourcePolicies() map[string]ArtifactSourcePolicy {
	return map[string]ArtifactSourcePolicy{
		"ums": {
			RepoSlug:        "phucle996/aurora-ums",
			BundleAssetBase: "aurora-ums",
		},
		"platform": {
			RepoSlug:        "phucle996/aurora-platform-resource",
			BundleAssetBase: "aurora-platform-resource",
		},
		"paas": {
			RepoSlug:        "phucle996/aurora-paas-service",
			BundleAssetBase: "aurora-paas-service",
		},
		"dbaas": {
			RepoSlug:        "phucle996/aurora-dbaas-module",
			BundleAssetBase: "aurora-dbaas-service",
		},
	}
}
