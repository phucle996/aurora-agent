package install

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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

	timeout := 40 * time.Minute
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
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
