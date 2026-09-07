package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// claudeEnvironment is process-local; it never logs out or rewrites live settings.
func claudeEnvironment(inherited []string, key, config string) []string {
	env := []string{}
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if upper == "HOME" || upper == "SSH_AUTH_SOCK" || upper == "SSH_AGENT_PID" || upper == "DOCKER_CONFIG" || upper == "KUBECONFIG" ||
			strings.HasPrefix(upper, "XDG_") || strings.HasPrefix(upper, "GIT_") || strings.HasPrefix(upper, "GH_") || strings.HasPrefix(upper, "GITHUB_") ||
			strings.HasPrefix(upper, "AWS_") || strings.HasPrefix(upper, "AZURE_") || strings.HasPrefix(upper, "GOOGLE_") || strings.HasPrefix(upper, "CLOUDSDK_") ||
			strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") || strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.HasPrefix(upper, "ANTHROPIC_") || strings.HasPrefix(upper, "CLAUDE") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+config, "XDG_CONFIG_HOME="+config, "GH_CONFIG_DIR="+config, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "ANTHROPIC_BASE_URL=https://openrouter.ai/api", "ANTHROPIC_AUTH_TOKEN="+key, "ANTHROPIC_API_KEY=", "CLAUDE_CONFIG_DIR="+config, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+config, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	for _, name := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		env = append(env, name+"="+model)
	}
	return env
}

func claudeArguments(cfg Config) []string {
	return []string{"-p", "--model", model, "--effort", cfg.Reasoning, "--output-format", "stream-json", "--verbose", "--max-budget-usd", strconv.FormatFloat(cfg.MaxCost, 'f', -1, 64), "--max-turns", strconv.Itoa(cfg.MaxCalls), "--permission-mode", "dontAsk", "--tools", "Bash,Read,Edit,Write,Glob,Grep", "--allowedTools", "Bash,Read,Edit,Write,Glob,Grep", "--disable-slash-commands", "--setting-sources", "", "--settings", `{"disableAllHooks":true,"autoMemoryEnabled":false}`, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-session-persistence", "--no-chrome"}
}

func runClaude(parent context.Context, cfg Config, output string, prompt []byte, maxOutputTokens int) error {
	return executeClaude(parent, cfg, output, prompt, maxOutputTokens, true)
}

func executeClaude(parent context.Context, cfg Config, output string, prompt []byte, maxOutputTokens int, ownProcessGroup bool) error {
	key, err := resolveKey(cfg.KeyFile)
	if err != nil {
		return err
	}
	config := filepath.Join(output, "claude-config")
	if err := os.Mkdir(config, 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(cfg.MaxSeconds)*time.Second)
	defer cancel()
	args := claudeArguments(cfg)
	if err := writeJSON(filepath.Join(output, "claude-invocation.json"), map[string]any{"argv": args, "model": model, "budget_basis": "Claude CLI estimate, not OpenRouter invoice; reconcile generation receipts", "timeout_seconds": cfg.MaxSeconds, "max_output_tokens": maxOutputTokens}); err != nil {
		return err
	}
	stdout, err := os.OpenFile(filepath.Join(output, "claude-stream.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(filepath.Join(output, "claude-stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = cfg.Workspace
	cmd.Env = claudeEnvironment(os.Environ(), key, config)
	if maxOutputTokens > 0 {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS="+strconv.Itoa(maxOutputTokens))
	}
	cmd.Stdin = strings.NewReader(string(prompt))
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if ownProcessGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
	}
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Claude execution (see attempt logs): %w", err)
	}
	return nil
}
