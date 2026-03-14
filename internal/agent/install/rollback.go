package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type backupState struct {
	Path       string
	Existed    bool
	BackupPath string
}

type installRollback struct {
	root    string
	backups map[string]backupState
}

func newInstallRollback(root string) *installRollback {
	return &installRollback{
		root:    strings.TrimSpace(root),
		backups: map[string]backupState{},
	}
}

func (r *installRollback) Capture(path string) error {
	if r == nil {
		return nil
	}
	target := strings.TrimSpace(path)
	if target == "" {
		return nil
	}
	if _, ok := r.backups[target]; ok {
		return nil
	}
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return fmt.Errorf("create rollback dir failed: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			r.backups[target] = backupState{Path: target, Existed: false}
			return nil
		}
		return fmt.Errorf("stat rollback target failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("rollback only supports regular files: %s", target)
	}
	backupPath := filepath.Join(r.root, fmt.Sprintf("%d.bak", len(r.backups)))
	if err := copyFile(target, backupPath, info.Mode()); err != nil {
		return fmt.Errorf("backup file failed: %w", err)
	}
	r.backups[target] = backupState{
		Path:       target,
		Existed:    true,
		BackupPath: backupPath,
	}
	return nil
}

func (r *installRollback) Restore(ctx context.Context, runner CommandRunner, manifest *ArtifactManifest, logFn InstallLogFn) error {
	if r == nil || len(r.backups) == 0 {
		return nil
	}
	var restoreErrs []error
	if manifest != nil && runner != nil {
		if serviceName := strings.TrimSpace(manifest.Service.Name); serviceName != "" {
			logInstallStage(logFn, InstallStageRollback, "stopping service before restoring files")
			_ = runner.Run(ctx, "systemctl", "stop", serviceName)
		}
	}
	for _, backup := range r.backups {
		if !backup.Existed {
			if err := os.Remove(strings.TrimSpace(backup.Path)); err != nil && !os.IsNotExist(err) {
				restoreErrs = append(restoreErrs, fmt.Errorf("remove %s failed: %w", backup.Path, err))
			}
			continue
		}
		info, err := os.Stat(backup.BackupPath)
		if err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("stat backup %s failed: %w", backup.BackupPath, err))
			continue
		}
		if err := copyFile(backup.BackupPath, backup.Path, info.Mode()); err != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore %s failed: %w", backup.Path, err))
		}
	}
	if manifest != nil && runner != nil {
		if err := runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
		if serviceName := strings.TrimSpace(manifest.Service.Name); serviceName != "" {
			if backup, ok := r.backups[strings.TrimSpace(manifest.Service.UnitPath)]; ok && backup.Existed {
				if err := runner.Run(ctx, "systemctl", "enable", serviceName); err != nil {
					restoreErrs = append(restoreErrs, err)
				}
				if err := runner.Run(ctx, "systemctl", "restart", serviceName); err != nil {
					restoreErrs = append(restoreErrs, err)
				}
			} else {
				_ = runner.Run(ctx, "systemctl", "stop", serviceName)
				_ = runner.Run(ctx, "systemctl", "disable", serviceName)
			}
		}
		if manifest.Nginx.Enabled {
			if err := runner.Run(ctx, "nginx", "-t"); err != nil {
				restoreErrs = append(restoreErrs, err)
			} else if err := runner.Run(ctx, "systemctl", "reload", "nginx"); err != nil {
				restoreErrs = append(restoreErrs, err)
			}
		}
	}
	if len(restoreErrs) > 0 {
		logInstallStage(logFn, InstallStageRollback, "rollback completed with errors")
		return errors.Join(restoreErrs...)
	}
	logInstallStage(logFn, InstallStageRollback, "rollback completed")
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	src, err := os.Open(strings.TrimSpace(source))
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(strings.TrimSpace(destination)), 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(strings.TrimSpace(destination), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
