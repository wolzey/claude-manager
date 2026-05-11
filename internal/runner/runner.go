package runner

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Options describes one `claude -p` invocation.
type Options struct {
	Prompt         string
	SessionID      string
	Resume         bool // false → --session-id (create); true → --resume (continue)
	Name           string
	Cwd            string
	AllowedTools   string
	Model          string
	BudgetUSD      float64
	PermissionMode string
	ExtraArgs      []string
	LogPath        string
	Stderr         io.Writer
}

// Run executes claude -p, tees stream-json to LogPath, and returns the parsed Result.
// Strips ANTHROPIC_API_KEY from the child env to force subscription-OAuth billing.
func Run(opts Options) (*Result, error) {
	if opts.PermissionMode == "" {
		opts.PermissionMode = "acceptEdits"
	}
	sessionFlag := "--session-id"
	if opts.Resume {
		sessionFlag = "--resume"
	}
	args := []string{
		"-p", opts.Prompt,
		sessionFlag, opts.SessionID,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", opts.PermissionMode,
	}
	if opts.Name != "" {
		args = append(args, "--name", opts.Name)
	}
	if strings.TrimSpace(opts.AllowedTools) != "" {
		args = append(args, "--allowed-tools", opts.AllowedTools)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.BudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", opts.BudgetUSD))
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.Command("claude", args...)
	cmd.Dir = opts.Cwd
	cmd.Env = scrubbedEnv(os.Environ())
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting claude: %w", err)
	}

	var logFile *os.File
	if opts.LogPath != "" {
		logFile, err = os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("opening log: %w", err)
		}
		defer logFile.Close()
	}

	result, parseErr := ParseStream(stdout, logFile)
	waitErr := cmd.Wait()
	if parseErr != nil && result == nil {
		if waitErr != nil {
			return nil, fmt.Errorf("claude exited (%v) without producing a result line: %w", waitErr, parseErr)
		}
		return nil, parseErr
	}
	if waitErr != nil && result != nil {
		result.IsError = true
	}
	return result, nil
}

func scrubbedEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
