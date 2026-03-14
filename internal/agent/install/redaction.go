package install

import (
	"regexp"
	"slices"
	"strings"
)

var pemBlockPattern = regexp.MustCompile(`-----BEGIN [^-]+-----[\s\S]*?-----END [^-]+-----`)

func redactInstallText(raw string, req InstallModuleRequest) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}

	text = pemBlockPattern.ReplaceAllString(text, "[REDACTED_PEM]")
	for _, secret := range collectInstallSecrets(req) {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return strings.TrimSpace(text)
}

func collectInstallSecrets(req InstallModuleRequest) []string {
	out := make([]string, 0, len(req.Env)+len(req.Files))
	seen := map[string]struct{}{}

	for key, value := range req.Env {
		if !isSensitiveInstallKey(key) && !looksSensitiveInstallValue(value) {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	for path, value := range req.Files {
		if !isSensitiveInstallKey(path) && !looksSensitiveInstallValue(value) {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	slices.SortFunc(out, func(a, b string) int {
		switch {
		case len(a) > len(b):
			return -1
		case len(a) < len(b):
			return 1
		default:
			return 0
		}
	})
	return out
}

func isSensitiveInstallKey(raw string) bool {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return false
	}
	sensitiveParts := []string{
		"secret",
		"token",
		"password",
		"passwd",
		"private",
		"cert",
		"key",
		"pem",
		"ca",
	}
	for _, part := range sensitiveParts {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

func looksSensitiveInstallValue(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	if strings.Contains(value, "-----BEGIN ") {
		return true
	}
	if len(value) >= 32 && !strings.ContainsAny(value, " \t\r\n") {
		return true
	}
	return false
}
