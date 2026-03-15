package install

import (
	agentcommandv1 "github.com/phucle996/aurora-proto/agentcommandv1"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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

func RunCommandStream(
	ctx context.Context,
	req *agentcommandv1.RunCommandRequest,
	send func(*agentcommandv1.RunCommandStreamEvent) error,
) error {
	if send == nil {
		return fmt.Errorf("run command stream sender is required")
	}
	if req == nil {
		return send(&agentcommandv1.RunCommandStreamEvent{
			Type:      "error",
			ErrorText: "command request is required",
		})
	}

	command := strings.TrimSpace(req.Command)
	if command == "" {
		return send(&agentcommandv1.RunCommandStreamEvent{
			Type:      "error",
			ErrorText: "command is required",
		})
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

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
	cmd.Env = envList

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return send(&agentcommandv1.RunCommandStreamEvent{
			Type:      "error",
			ErrorText: strings.TrimSpace(err.Error()),
		})
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return send(&agentcommandv1.RunCommandStreamEvent{
			Type:      "error",
			ErrorText: strings.TrimSpace(err.Error()),
		})
	}

	if err := cmd.Start(); err != nil {
		return send(&agentcommandv1.RunCommandStreamEvent{
			Type:      "error",
			ErrorText: strings.TrimSpace(err.Error()),
		})
	}

	var sendMu sync.Mutex
	safeSend := func(event *agentcommandv1.RunCommandStreamEvent) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return send(event)
	}

	readPipe := func(streamName string, reader io.Reader, done chan<- error) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			if err := safeSend(&agentcommandv1.RunCommandStreamEvent{
				Type:    "log",
				Stream:  streamName,
				Message: scanner.Text(),
			}); err != nil {
				done <- err
				return
			}
		}
		done <- scanner.Err()
	}

	streamErrCh := make(chan error, 2)
	go readPipe("stdout", stdoutPipe, streamErrCh)
	go readPipe("stderr", stderrPipe, streamErrCh)

	waitErr := cmd.Wait()
	errA := <-streamErrCh
	errB := <-streamErrCh
	if errA != nil {
		return errA
	}
	if errB != nil {
		return errB
	}

	exitCode := int32(0)
	if waitErr == nil {
		return safeSend(&agentcommandv1.RunCommandStreamEvent{
			Type:     "result",
			ExitCode: exitCode,
		})
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		exitCode = int32(exitErr.ExitCode())
	} else {
		exitCode = -1
	}

	errorText := ""
	if runCtx.Err() == context.DeadlineExceeded {
		errorText = "command timeout exceeded"
	} else if statusErr, ok := ggrpcstatus.FromError(waitErr); ok {
		errorText = strings.TrimSpace(statusErr.Message())
	} else {
		errorText = strings.TrimSpace(waitErr.Error())
	}
	if errorText == "" {
		errorText = fmt.Sprintf("command failed exit_code=%d", exitCode)
	}
	return safeSend(&agentcommandv1.RunCommandStreamEvent{
		Type:      "error",
		ExitCode:  exitCode,
		ErrorText: errorText,
	})
}
