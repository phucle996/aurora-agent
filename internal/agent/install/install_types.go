package install

type RunCommandRequest struct {
	Command        string            `json:"command"`
	TimeoutSeconds int32             `json:"timeout_seconds,omitempty"`
	InstallRuntime string            `json:"install_runtime,omitempty"` // linux | k8s
	Kubeconfig     string            `json:"kubeconfig,omitempty"`
	KubeconfigPath string            `json:"kubeconfig_path,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

type RunCommandResponse struct {
	OK        bool   `json:"ok"`
	ExitCode  int32  `json:"exit_code"`
	Output    string `json:"output"`
	ErrorText string `json:"error_text,omitempty"`
}
