package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ggrpcstatus "google.golang.org/grpc/status"
)

func RunCommand(ctx context.Context, req *RunCommandRequest) (*RunCommandResponse, error) {
	if req == nil {
		return &RunCommandResponse{
			OK:       false,
			ExitCode: -1,
		}, nil
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return &RunCommandResponse{
			OK:        false,
			ExitCode:  -1,
			ErrorText: "command is required",
		}, nil
	}

	installRuntime := strings.ToLower(strings.TrimSpace(req.InstallRuntime))
	if installRuntime == "" {
		installRuntime = "linux"
	}
	if installRuntime != "linux" && installRuntime != "k8s" {
		return &RunCommandResponse{
			OK:        false,
			ExitCode:  -1,
			ErrorText: "install_runtime must be linux or k8s",
		}, nil
	}

	timeout := 40 * time.Minute
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	envList := os.Environ()
	for key, value := range req.Env {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		envList = append(envList, trimmedKey+"="+value)
	}
	envList = append(envList, "AURORA_INSTALL_RUNTIME="+installRuntime)

	var kubeconfigTempPath string
	if installRuntime == "k8s" {
		kubeconfigContent := strings.TrimSpace(req.Kubeconfig)
		kubeconfigPath := strings.TrimSpace(req.KubeconfigPath)

		if kubeconfigContent != "" {
			tmpDir, err := os.MkdirTemp("", "aurora-agent-kubeconfig-*")
			if err != nil {
				return &RunCommandResponse{
					OK:        false,
					ExitCode:  -1,
					ErrorText: "cannot create temp dir for kubeconfig",
				}, nil
			}
			defer os.RemoveAll(tmpDir)

			kubeconfigTempPath = filepath.Join(tmpDir, "config")
			if err := os.WriteFile(kubeconfigTempPath, []byte(kubeconfigContent+"\n"), 0o600); err != nil {
				return &RunCommandResponse{
					OK:        false,
					ExitCode:  -1,
					ErrorText: "cannot write kubeconfig content",
				}, nil
			}
			kubeconfigPath = kubeconfigTempPath
		}

		if strings.TrimSpace(kubeconfigPath) == "" {
			kubeconfigPath = os.Getenv("KUBECONFIG")
		}
		if strings.TrimSpace(kubeconfigPath) == "" {
			return &RunCommandResponse{
				OK:        false,
				ExitCode:  -1,
				ErrorText: "k8s runtime requires kubeconfig or kubeconfig_path",
			}, nil
		}
		if _, err := os.Stat(kubeconfigPath); err != nil {
			return &RunCommandResponse{
				OK:        false,
				ExitCode:  -1,
				ErrorText: "kubeconfig file is not accessible",
			}, nil
		}
		envList = append(envList, "KUBECONFIG="+kubeconfigPath)
	}

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
	cmd.Env = envList
	output, err := cmd.CombinedOutput()
	result := &RunCommandResponse{
		ExitCode: 0,
		Output:   string(output),
		OK:       err == nil,
	}
	if err == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = int32(exitErr.ExitCode())
	} else {
		result.ExitCode = -1
	}

	if runCtx.Err() == context.DeadlineExceeded {
		result.ErrorText = "command timeout exceeded"
	} else if statusErr, ok := ggrpcstatus.FromError(err); ok {
		result.ErrorText = strings.TrimSpace(statusErr.Message())
	} else {
		result.ErrorText = strings.TrimSpace(err.Error())
	}
	if result.ErrorText == "" {
		result.ErrorText = fmt.Sprintf("command failed exit_code=%d", result.ExitCode)
	}
	return result, nil
}
