package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (e *Engine) downloadArtifact(ctx context.Context, artifactURL string, dstPath string) error {
	policy := currentPolicy()
	if err := validateArtifactURL(artifactURL, policy.AllowedHosts); err != nil {
		return err
	}
	client := e.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	cloned := *client
	cloned.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateArtifactURL(req.URL.String(), policy.AllowedHosts); err != nil {
			return err
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after too many redirects")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(artifactURL), nil)
	if err != nil {
		return fmt.Errorf("build artifact request failed: %w", err)
	}
	resp, err := cloned.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download artifact failed: unexpected status %d", resp.StatusCode)
	}
	if artifactDownloadMaxBytes > 0 && resp.ContentLength > artifactDownloadMaxBytes {
		return fmt.Errorf("artifact is too large")
	}

	file, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create artifact file failed: %w", err)
	}
	defer file.Close()

	reader := io.Reader(resp.Body)
	if artifactDownloadMaxBytes > 0 {
		reader = io.LimitReader(resp.Body, artifactDownloadMaxBytes+1)
	}
	written, err := io.Copy(file, reader)
	if err != nil {
		return fmt.Errorf("write artifact file failed: %w", err)
	}
	if artifactDownloadMaxBytes > 0 && written > artifactDownloadMaxBytes {
		return fmt.Errorf("artifact is too large")
	}
	return nil
}

func verifyArtifactChecksum(artifactPath string, expected string) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return fmt.Errorf("artifact checksum is required")
	}
	if strings.HasPrefix(strings.ToLower(expected), "sha256:") {
		expected = strings.TrimSpace(expected[len("sha256:"):])
	}

	file, err := os.Open(strings.TrimSpace(artifactPath))
	if err != nil {
		return fmt.Errorf("open artifact failed: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash artifact failed: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("artifact checksum mismatch")
	}
	return nil
}

func unpackTarGz(archivePath string, dstDir string) error {
	file, err := os.Open(strings.TrimSpace(archivePath))
	if err != nil {
		return fmt.Errorf("open artifact archive failed: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip archive failed: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive failed: %w", err)
		}

		name := filepath.Clean(strings.TrimSpace(header.Name))
		if name == "." || name == "" {
			continue
		}
		targetPath := filepath.Join(dstDir, name)
		if !strings.HasPrefix(targetPath, filepath.Clean(dstDir)+string(os.PathSeparator)) && filepath.Clean(targetPath) != filepath.Clean(dstDir) {
			return fmt.Errorf("archive entry escapes destination: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("create dir from archive failed: %w", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("create parent dir failed: %w", err)
			}
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("create archive file failed: %w", err)
			}
			if _, err := io.Copy(out, tarReader); err != nil {
				out.Close()
				return fmt.Errorf("write archive file failed: %w", err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close archive file failed: %w", err)
			}
		default:
			continue
		}
	}
	return nil
}
