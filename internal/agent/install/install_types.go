package install

type RunCommandRequest struct {
	Command        string `json:"command"`
	TimeoutSeconds int32  `json:"timeout_seconds,omitempty"`
}

type RunCommandResponse struct {
	OK        bool   `json:"ok"`
	ExitCode  int32  `json:"exit_code"`
	Output    string `json:"output"`
	ErrorText string `json:"error_text,omitempty"`
}
