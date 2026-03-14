package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	installedArtifactCacheDir       = "/var/lib/aurora-agent/artifact-cache"
	artifactCacheMaxBytes     int64 = 2 * 1024 * 1024 * 1024
	artifactDownloadMaxBytes  int64 = 512 * 1024 * 1024
	artifactCacheMu           sync.Mutex
)

type cachedArtifactFile struct {
	path string
	size int64
}

func (e *Engine) ensureArtifactAvailable(ctx context.Context, artifactURL string, checksum string) (string, bool, error) {
	cachePath, err := artifactCachePath(artifactURL, checksum)
	if err != nil {
		return "", false, err
	}

	artifactCacheMu.Lock()
	defer artifactCacheMu.Unlock()

	if err := os.MkdirAll(installedArtifactCacheDir, 0o755); err != nil {
		return "", false, fmt.Errorf("create artifact cache dir failed: %w", err)
	}

	if _, statErr := os.Stat(cachePath); statErr == nil {
		if err := verifyArtifactChecksum(cachePath, checksum); err == nil {
			return cachePath, true, nil
		}
		_ = os.Remove(cachePath)
	}

	tmpFile, err := os.CreateTemp(installedArtifactCacheDir, "download-*.tmp")
	if err != nil {
		return "", false, fmt.Errorf("create artifact cache temp file failed: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := e.downloadArtifact(ctx, artifactURL, tmpPath); err != nil {
		return "", false, err
	}
	if err := verifyArtifactChecksum(tmpPath, checksum); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		return "", false, fmt.Errorf("persist artifact cache failed: %w", err)
	}
	if err := pruneArtifactCache(); err != nil {
		return "", false, err
	}
	return cachePath, false, nil
}

func artifactCachePath(artifactURL string, checksum string) (string, error) {
	expected := normalizedChecksumValue(checksum)
	if expected == "" {
		return "", fmt.Errorf("artifact checksum is required")
	}
	baseName := filepath.Base(strings.TrimSpace(artifactURL))
	if baseName == "." || strings.TrimSpace(baseName) == "" || strings.Contains(baseName, string(os.PathSeparator)) {
		baseName = "bundle.tar.gz"
	}
	keyHash := sha256.Sum256([]byte(strings.TrimSpace(artifactURL) + "|" + expected))
	fileName := fmt.Sprintf("%s-%s", hex.EncodeToString(keyHash[:8]), baseName)
	return filepath.Join(installedArtifactCacheDir, fileName), nil
}

func normalizedChecksumValue(raw string) string {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(value), "sha256:") {
		value = strings.TrimSpace(value[len("sha256:"):])
	}
	return strings.ToLower(value)
}

func pruneArtifactCache() error {
	if artifactCacheMaxBytes <= 0 {
		return nil
	}
	entries, err := os.ReadDir(installedArtifactCacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read artifact cache dir failed: %w", err)
	}

	files := make([]cachedArtifactFile, 0, len(entries))
	var totalSize int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		totalSize += info.Size()
		files = append(files, cachedArtifactFile{
			path: filepath.Join(installedArtifactCacheDir, entry.Name()),
			size: info.Size(),
		})
	}
	if totalSize <= artifactCacheMaxBytes {
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		leftInfo, leftErr := os.Stat(files[i].path)
		rightInfo, rightErr := os.Stat(files[j].path)
		if leftErr != nil || rightErr != nil {
			return files[i].path < files[j].path
		}
		return leftInfo.ModTime().Before(rightInfo.ModTime())
	})

	for _, file := range files {
		if totalSize <= artifactCacheMaxBytes {
			break
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			continue
		}
		totalSize -= file.size
	}
	return nil
}
