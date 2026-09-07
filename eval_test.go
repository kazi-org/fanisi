package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeEnvironmentIsolatesCredentials(t *testing.T) {
	t.Parallel()
	inherited := []string{"PATH=/bin", "HOME=/home/operator", "XDG_CONFIG_HOME=/live/xdg", "GH_CONFIG_DIR=/live/gh", "SSH_AUTH_SOCK=/live/agent", "GIT_CONFIG_GLOBAL=/live/gitconfig", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_VALUE_0=secret-helper", "AWS_SHARED_CREDENTIALS_FILE=/live/aws", "ANTHROPIC_API_KEY=subscription-secret", "CLAUDE_CODE_OAUTH_TOKEN=subscription-token", "OPENROUTER_API_KEY=another-key", "CLAUDE_CONFIG_DIR=/live/config", "CLAUDECODE=1"}
	env := strings.Join(claudeEnvironment(inherited, "test-router-key", "/isolated/config"), "\n")
	for _, forbidden := range []string{"subscription-secret", "subscription-token", "another-key", "/live/config", "CLAUDECODE=", "/home/operator", "/live/", "SSH_AUTH_SOCK=", "GIT_CONFIG_COUNT=", "secret-helper"} {
		if strings.Contains(env, forbidden) {
			t.Fatalf("inherited auth/session setting leaked: %s", forbidden)
		}
	}
	for _, required := range []string{"HOME=/isolated/config", "XDG_CONFIG_HOME=/isolated/config", "GH_CONFIG_DIR=/isolated/config", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "ANTHROPIC_API_KEY=\n", "ANTHROPIC_AUTH_TOKEN=test-router-key", "ANTHROPIC_DEFAULT_HAIKU_MODEL=" + model, "CLAUDE_CONFIG_DIR=/isolated/config"} {
		if !strings.Contains(env, required) {
			t.Fatalf("missing isolated configuration: %s", required)
		}
	}
	if inherited[1] != "HOME=/home/operator" {
		t.Fatal("modified caller environment")
	}
}

func TestEvaluationUsesFrozenWorktreeAndIndependentVerifier(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	bin := filepath.Join(root, "bin")
	for _, p := range []string{repo, bin} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "test"}, {"config", "user.email", "test@example.invalid"}, {"config", "core.hooksPath", "/dev/null"}, {"config", "commit.gpgsign", "false"}} {
		if _, err := gitOutput(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "value.txt"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(ctx, repo, "add", "value.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(ctx, repo, "commit", "-qm", "base"); err != nil {
		t.Fatal(err)
	}
	base, err := gitOutput(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(root, "prompt.md")
	key := filepath.Join(root, "key.env")
	if err := os.WriteFile(prompt, []byte("Replace old with new."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("OPENROUTER_API_KEY=fixture-only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_CONFIG_DIR", filepath.Join(root, "live-gh"))
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(root, "live-agent"))
	// This executable is an offline test double; no API or real Claude process runs.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n[ \"$HOME\" = \"$CLAUDE_CONFIG_DIR\" ] || exit 21\n[ \"$GH_CONFIG_DIR\" = \"$CLAUDE_CONFIG_DIR\" ] || exit 22\n[ -z \"${SSH_AUTH_SOCK+x}\" ] || exit 23\n[ \"$GIT_CONFIG_GLOBAL\" = /dev/null ] || exit 24\ncat >/dev/null\nprintf 'new\\n' > value.txt\nprintf '{\"type\":\"result\",\"is_error\":false}\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := Config{SchemaVersion: 1, Workspace: repo, Output: filepath.Join(root, "unused"), KeyFile: key, PromptFile: prompt, WritePaths: []string{"value.txt"}, VerifyCommand: []string{"sh", "-c", "test \"$(cat value.txt)\" = new"}, MaxCalls: 2, MaxSeconds: 20, MaxCost: 1, MaxTokens: 10000, MaxOutputTokens: 100, MaxContextBytes: 10000, PacketBytes: 4000, Reasoning: "medium", Pricing: Pricing{Input: 1, Output: 1, Source: "test"}}
	task := filepath.Join(root, "task.json")
	if err := writeJSON(task, cfg); err != nil {
		t.Fatal(err)
	}
	e := Evaluation{ClaudeMaxOutputTokens: 8192, ClaudeMaxEstimatedCost: 2, SchemaVersion: 1, TaskID: "fixture", Repository: repo, Base: strings.TrimSpace(string(base)), WorktreeRoot: filepath.Join(root, "worktrees"), Output: filepath.Join(root, "out"), TaskConfig: task}
	manifest := filepath.Join(root, "eval.json")
	if err := writeJSON(manifest, e); err != nil {
		t.Fatal(err)
	}
	if err := evalRun(ctx, manifest, "claude", 1); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.Output, e.TaskID, "claude", "1")
	var a Attempt
	if err := readJSON(filepath.Join(dir, "attempt.json"), &a); err != nil {
		t.Fatal(err)
	}
	if a.Status != "verified_pending_review" || !a.ScopeOK || !a.VerificationPassed || a.BaselineExit != 1 || a.PatchSHA == "" {
		t.Fatalf("bad candidate verdict: %+v", a)
	}
	var invocation struct {
		MaxOutputTokens int `json:"max_output_tokens"`
	}
	rawInvocation, err := os.ReadFile(filepath.Join(dir, "claude-invocation.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawInvocation, &invocation); err != nil {
		t.Fatal(err)
	}
	if invocation.MaxOutputTokens != 8192 {
		t.Fatal("explicit output cap was not forwarded")
	}
	original, err := os.ReadFile(filepath.Join(repo, "value.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "old\n" {
		t.Fatal("original workspace changed")
	}
	if err := recordReview(ctx, dir, "accept", "test-reviewer", "agent", 12, "Reviewed behavior and scope."); err != nil {
		t.Fatal(err)
	}
	outOfScope := filepath.Join(a.Workspace, "unreviewed.txt")
	if err := os.WriteFile(outOfScope, []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recordReview(ctx, dir, "accept", "test-reviewer", "agent", 1, "Attempting acceptance with an extra file."); err == nil {
		t.Fatal("accepted a new out-of-scope file after verification")
	}
	if err := os.Remove(outOfScope); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Workspace, "value.txt"), []byte("later edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recordReview(ctx, dir, "accept", "test-reviewer", "agent", 1, "Attempting stale acceptance."); err == nil {
		t.Fatal("accepted a patch changed after verification")
	}
	if err := evalRun(ctx, manifest, "claude", 1); err == nil {
		t.Fatal("overwrote existing attempt")
	}
	// A repair starts from the recorded candidate, not the parent's later edits.
	cfg.VerifyCommand = []string{"sh", "-c", "test \"$(cat value.txt)\" = repaired"}
	repairTask := filepath.Join(root, "repair-task.json")
	if err := writeJSON(repairTask, cfg); err != nil {
		t.Fatal(err)
	}
	e.TaskConfig = repairTask
	e.RepairFrom = dir
	if err := writeJSON(manifest, e); err != nil {
		t.Fatal(err)
	}
	worker := "#!/bin/sh\ncat >/dev/null\ntest \"$(cat value.txt)\" = new || exit 24\nprintf 'repaired\\n' > value.txt\nprintf '{\"type\":\"result\",\"is_error\":false}\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(worker), 0700); err != nil {
		t.Fatal(err)
	}
	if err := evalRun(ctx, manifest, "claude", 2); err != nil {
		t.Fatal(err)
	}
	var repair Attempt
	if err := readJSON(filepath.Join(e.Output, "fixture", "claude", "2", "attempt.json"), &repair); err != nil {
		t.Fatal(err)
	}
	if repair.RepairFrom != dir || repair.BaselineExit != 1 || repair.Status != "verified_pending_review" || repair.PatchSHA == a.PatchSHA {
		t.Fatalf("invalid repair evidence: %+v", repair)
	}
	// The controller bridge runs the same isolated worker and retains its terminal.
	bridgeCfg := cfg
	bridgeCfg.Workspace = repair.Workspace
	bridgeTask := filepath.Join(root, "bridge-task.json")
	if err := writeJSON(bridgeTask, bridgeCfg); err != nil {
		t.Fatal(err)
	}
	bridgeOutput := filepath.Join(root, "bridge-dispatches")
	bridgeArgs := []string{"-p", "controller task", "--output-format", "json", "--allowed-tools", "Bash", "Read", "Edit", "Write", "Glob", "Grep", "--permission-mode", "dontAsk", "--model", model, "--tools", "Read", "Edit", "Write", "Bash", "Glob", "Grep", "--strict-mcp-config", "--effort", "medium"}
	if err := claudeBridge(ctx, bridgeTask, bridgeOutput, 8192, 2, "", bridgeArgs); err == nil {
		t.Fatal("bridge accepted the wrong working directory")
	}
	t.Chdir(repair.Workspace)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\ncat >/dev/null\nprintf '{\"type\":\"result\",\"is_error\":false,\"result\":\"controller fixture\"}\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := claudeBridge(ctx, bridgeTask, bridgeOutput, 8192, 2, "", bridgeArgs); err != nil {
		t.Fatal(err)
	}
	streams, err := filepath.Glob(filepath.Join(bridgeOutput, "dispatch-*", "claude-stream.jsonl"))
	if err != nil || len(streams) != 1 {
		t.Fatalf("bridge did not retain one dispatch: %v %v", streams, err)
	}
	var terminal struct {
		Result string `json:"result"`
	}
	if found, err := claudeTerminal(streams[0], &terminal); err != nil || !found || terminal.Result != "controller fixture" {
		t.Fatalf("bridge terminal changed: %+v %v", terminal, err)
	}
	e.Base = "HEAD"
	if err := writeJSON(manifest, e); err != nil {
		t.Fatal(err)
	}
	if err := evalRun(ctx, manifest, "claude", 2); err == nil {
		t.Fatal("accepted mutable baseline")
	}
}
