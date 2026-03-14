package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

func renderTemplateFile(templatePath string, destinationPath string, data templateData) error {
	raw, err := os.ReadFile(strings.TrimSpace(templatePath))
	if err != nil {
		return fmt.Errorf("read template failed: %w", err)
	}

	tpl, err := template.New(filepath.Base(templatePath)).Option("missingkey=zero").Parse(string(raw))
	if err != nil {
		return fmt.Errorf("parse template failed: %w", err)
	}

	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return fmt.Errorf("render template failed: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("create template destination dir failed: %w", err)
	}
	if err := os.WriteFile(destinationPath, rendered.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write rendered template failed: %w", err)
	}
	return nil
}

func writeEnvFile(path string, env map[string]string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("env file path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create env dir failed: %w", err)
	}

	keys := make([]string, 0, len(env))
	for key := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		value := strings.ReplaceAll(env[key], "\n", "")
		value = strings.ReplaceAll(value, "\r", "")
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		return fmt.Errorf("write env file failed: %w", err)
	}
	return nil
}
