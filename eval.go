package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Evaluation names a frozen task once; every arm starts from the same commit.
type Evaluation struct {
	ClaudeMaxOutputTokens  int      `json:"claude_max_output_tokens,omitempty"`
	RepairFrom             string   `json:"repair_from,omitempty"`
	ClaudeMaxEstimatedCost float64  `json:"claude_max_estimated_cost_usd"`
	SchemaVersion          int      `json:"schema_version"`
	TaskID                 string   `json:"task_id"`
	Repository             string   `json:"repository"`
	Base                   string   `json:"base"`
	WorktreeRoot           string   `json:"worktree_root"`
	Output                 string   `json:"output"`
	TaskConfig             string   `json:"task_config"`
	ProtectedFiles         []string `json:"protected_files"`
	PreparationSeconds     *float64 `json:"preparation_seconds"`
}

type Attempt struct {
	RepairFrom          string            `json:"repair_from,omitempty"`
	SchemaVersion       int               `json:"schema_version"`
	TaskID              string            `json:"task_id"`
	Arm                 string            `json:"arm"`
	Number              int               `json:"attempt"`
	Base                string            `json:"base"`
	Branch              string            `json:"branch"`
	Workspace           string            `json:"workspace"`
	StartedAt           time.Time         `json:"started_at"`
	WallSeconds         float64           `json:"wall_seconds"`
	PreparationSeconds  *float64          `json:"preparation_seconds"`
	ExecutionSeconds    float64           `json:"execution_seconds"`
	VerificationSeconds float64           `json:"verification_seconds"`
	Status              string            `json:"status"`
	Error               string            `json:"error,omitempty"`
	PatchSHA            string            `json:"patch_sha256"`
	ScopeOK             bool              `json:"scope_ok"`
	VerificationPassed  bool              `json:"verification_passed"`
	BaselineExit        int               `json:"baseline_exit"`
	ConfigurationSHA    string            `json:"configuration_sha256"`
	PromptSHA           string            `json:"prompt_sha256"`
	Protected           map[string]string `json:"protected_files"`
}

func loadEvaluation(path string) (Evaluation, Config, error) {
	var e Evaluation
	if err := readStrictJSON(path, &e); err != nil {
		return e, Config{}, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return e, Config{}, err
	}
	for _, p := range []*string{&e.Repository, &e.WorktreeRoot, &e.Output, &e.TaskConfig, &e.RepairFrom} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	for i, p := range e.ProtectedFiles {
		if !filepath.IsAbs(p) {
			e.ProtectedFiles[i] = filepath.Join(base, p)
		}
	}
	if e.SchemaVersion != schemaVersion || !simpleID(e.TaskID) || e.Base == "" || strings.HasPrefix(e.Base, "-") || e.Repository == "" || e.WorktreeRoot == "" || e.Output == "" || (e.PreparationSeconds != nil && *e.PreparationSeconds < 0) {
		return e, Config{}, errors.New("invalid evaluation schema, task id, base, paths or preparation time")
	}
	if e.ClaudeMaxOutputTokens != 0 && (e.ClaudeMaxOutputTokens < 1024 || e.ClaudeMaxOutputTokens > 64000) {
		return e, Config{}, errors.New("Claude output token cap must be zero (CLI default) or 1024..64000")
	}
	cfg, err := loadConfig(e.TaskConfig)
	return e, cfg, err
}

func simpleID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return b, nil
}

func protectedHashes(paths []string) (map[string]string, error) {
	result := map[string]string{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		result[p] = digest(b)
	}
	return result, nil
}

func evalRun(ctx context.Context, path, arm string, number int) (runErr error) {
	if arm != "fanisi" && arm != "claude" && arm != "claude-packet" {
		return errors.New("arm must be fanisi, claude, or claude-packet")
	}
	if number < 1 || number > 100 {
		return errors.New("attempt must be 1..100")
	}
	e, cfg, err := loadEvaluation(path)
	if err != nil {
		return err
	}
	if arm != "fanisi" && (e.ClaudeMaxEstimatedCost <= 0 || e.ClaudeMaxEstimatedCost > 100) {
		return errors.New("Claude arms require an explicit claude_max_estimated_cost_usd (0..100); this is not provider spend")
	}
	base, err := gitOutput(ctx, e.Repository, "rev-parse", "--verify", e.Base+"^{commit}")
	if err != nil {
		return err
	}
	// Require an immutable SHA in the manifest; moving branch tips are not paired trials.
	sha := strings.TrimSpace(string(base))
	if e.Base != sha {
		return errors.New("evaluation base must be the full frozen commit SHA")
	}
	output := filepath.Join(e.Output, e.TaskID, arm, strconv.Itoa(number))
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		return err
	}
	if err := os.Mkdir(output, 0700); err != nil {
		return fmt.Errorf("fresh attempt directory: %w", err)
	}
	started := time.Now()
	cfg.Workspace = filepath.Join(e.WorktreeRoot, "fanisi-"+e.TaskID+"-"+arm+"-"+strconv.Itoa(number))
	cfg.Output = filepath.Join(output, "run")
	a := Attempt{RepairFrom: e.RepairFrom, SchemaVersion: schemaVersion, TaskID: e.TaskID, Arm: arm, Number: number, Base: sha, Branch: fmt.Sprintf("experiment/fanisi/%s/%s/%d", e.TaskID, arm, number), Workspace: cfg.Workspace, StartedAt: started.UTC(), PreparationSeconds: e.PreparationSeconds, Status: "failed", BaselineExit: -1}
	defer func() {
		a.WallSeconds = time.Since(started).Seconds()
		if runErr != nil {
			a.Error = runErr.Error()
		}
		if err := writeJSON(filepath.Join(output, "attempt.json"), a); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(a); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()
	raw, err := json.Marshal(struct {
		Evaluation Evaluation
		Task       Config
	}{e, cfg})
	if err != nil {
		return err
	}
	a.ConfigurationSHA = digest(raw)
	if err := writeNew(filepath.Join(output, "configuration.json"), raw); err != nil {
		return err
	}
	a.Protected, err = protectedHashes(append(append([]string{}, e.ProtectedFiles...), cfg.PromptFile, e.TaskConfig))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.WorktreeRoot, 0755); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, e.Repository, "worktree", "add", "-b", a.Branch, cfg.Workspace, sha); err != nil {
		return err
	}
	if e.RepairFrom != "" {
		var parent Attempt
		if err := readJSON(filepath.Join(e.RepairFrom, "attempt.json"), &parent); err != nil {
			return err
		}
		if parent.TaskID != a.TaskID || parent.Arm != a.Arm || parent.Base != a.Base {
			return errors.New("repair parent must be the same task, arm, and frozen base")
		}
		patchPath := filepath.Join(e.RepairFrom, "candidate.patch")
		patch, err := os.ReadFile(patchPath)
		if err != nil {
			return err
		}
		if digest(patch) != parent.PatchSHA {
			return errors.New("repair parent patch hash mismatch")
		}
		a.Protected[patchPath] = digest(patch)
		cmd := exec.CommandContext(ctx, "git", "apply", "--binary", "-")
		cmd.Dir = cfg.Workspace
		cmd.Stdin = strings.NewReader(string(patch))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("apply verified parent artifact: %w", err)
		}
	}
	// Preserve worktrees, including failed attempts, for inspection and repair.
	_, a.BaselineExit, err = runCommand(ctx, cfg.Workspace, cfg.VerifyCommand, filepath.Join(output, "baseline.log"))
	if err != nil {
		return err
	}
	if a.BaselineExit != 1 {
		return fmt.Errorf("baseline verifier must reproduce the defect with exit 1; got %d", a.BaselineExit)
	}
	initial, err := snapshot(cfg)
	if err != nil {
		return err
	}
	brief, err := os.ReadFile(cfg.PromptFile)
	if err != nil {
		return err
	}
	contract := "\nEvaluation contract: work only in this worktree. The coordinator owns claims, review and landing. Do not commit, reset, push, change branches, access credentials, contact services, spawn agents, or inspect sibling worktrees. Leave the patch uncommitted. Change only these paths: " + strings.Join(cfg.WritePaths, ", ") + ". Preserve existing tests and requirements. The external verifier is fixed; do not edit it. Run verification with this argv: "
	argv, err := json.Marshal(cfg.VerifyCommand)
	if err != nil {
		return err
	}
	contract += string(argv) + "\n"
	brief = append(brief, []byte(contract)...)
	promptFile := filepath.Join(output, "brief.md")
	if err := writeNew(promptFile, brief); err != nil {
		return err
	}
	cfg.PromptFile = promptFile
	prompt := brief
	if arm != "claude" {
		prompt, _, err = buildPacket(ctx, cfg)
		if err != nil {
			return err
		}
	}
	a.PromptSHA = digest(prompt)
	if err := writeNew(filepath.Join(output, "submitted-prompt.md"), prompt); err != nil {
		return err
	}
	executionStart := time.Now()
	if arm == "fanisi" {
		runErr = run(ctx, cfg)
	} else {
		claudeCfg := cfg
		claudeCfg.MaxCost = e.ClaudeMaxEstimatedCost
		runErr = runClaude(ctx, claudeCfg, output, prompt, e.ClaudeMaxOutputTokens)
	}
	a.ExecutionSeconds = time.Since(executionStart).Seconds()
	checkStart := time.Now()
	// Run independently even after a worker failure; capture useful partial candidates.
	_, exit, verifyErr := runCommand(ctx, cfg.Workspace, cfg.VerifyCommand, filepath.Join(output, "verification.log"))
	a.VerificationSeconds = time.Since(checkStart).Seconds()
	a.VerificationPassed = exit == 0 && verifyErr == nil
	current, err := snapshot(cfg)
	if err != nil {
		return errors.Join(runErr, err)
	}
	if !changed(initial, current) {
		return errors.Join(runErr, errors.New("no scoped change produced"))
	}
	protectedPaths := make([]string, 0, len(a.Protected))
	for p := range a.Protected {
		protectedPaths = append(protectedPaths, p)
	}
	now, err := protectedHashes(protectedPaths)
	if err != nil {
		return errors.Join(runErr, err)
	}
	for p, hash := range now {
		if a.Protected[p] != hash {
			return errors.Join(runErr, fmt.Errorf("protected input changed: %s", p))
		}
	}
	a.ScopeOK, err = scopeUnchanged(ctx, cfg, sha)
	if err != nil {
		return errors.Join(runErr, err)
	}
	patch, err := candidatePatch(ctx, cfg)
	if err != nil {
		return errors.Join(runErr, err)
	}
	a.PatchSHA = digest(patch)
	if err := writeNew(filepath.Join(output, "candidate.patch"), patch); err != nil {
		return errors.Join(runErr, err)
	}
	if !a.ScopeOK || !a.VerificationPassed {
		return errors.Join(runErr, verifyErr, errors.New("candidate failed independent scope or verification checks"))
	}
	a.Status = "verified_pending_review"
	return runErr
}

func candidatePatch(ctx context.Context, cfg Config) ([]byte, error) {
	patch, err := gitOutput(ctx, cfg.Workspace, "diff", "--binary", "HEAD")
	if err != nil {
		return nil, err
	}
	// Include explicitly allowed new files without staging the worker's changes.
	for _, p := range cfg.WritePaths {
		if _, err := gitOutput(ctx, cfg.Workspace, "ls-files", "--error-unmatch", "--", p); err == nil {
			continue
		}
		path, err := scopedPath(cfg.Workspace, p, cfg.WritePaths)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, "git", "diff", "--no-index", "--binary", "--", "/dev/null", p)
		cmd.Dir = cfg.Workspace
		b, err := cmd.Output()
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
			return nil, err
		}
		patch = append(patch, b...)
	}
	return patch, nil
}

// Recheck the whole workspace, including new files, at verification and review.
func scopeUnchanged(ctx context.Context, cfg Config, sha string) (bool, error) {
	changedPaths, err := gitOutput(ctx, cfg.Workspace, "diff", "--name-only", "-z", sha)
	if err != nil {
		return false, err
	}
	untracked, err := gitOutput(ctx, cfg.Workspace, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return false, err
	}
	scopeOK := true
	allowed := map[string]bool{}
	for _, p := range cfg.WritePaths {
		allowed[p] = true
	}
	for _, p := range strings.Split(string(append(changedPaths, untracked...)), "\x00") {
		if p != "" && !allowed[p] {
			scopeOK = false
		}
	}
	head, err := gitOutput(ctx, cfg.Workspace, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(head)) != sha {
		scopeOK = false
	}
	return scopeOK, nil
}
