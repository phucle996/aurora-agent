package install

import agentcommandv1 "github.com/phucle996/aurora-proto/agentcommandv1"

type RunCommandRequest struct {
	Command        string            `json:"command"`
	TimeoutSeconds int32             `json:"timeout_seconds,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

type RunCommandResponse struct {
	OK        bool   `json:"ok"`
	ExitCode  int32  `json:"exit_code"`
	Output    string `json:"output"`
	ErrorText string `json:"error_text,omitempty"`
}

type RunCommandStreamRequest = agentcommandv1.RunCommandRequest
type RunCommandStreamEvent = agentcommandv1.RunCommandStreamEvent
