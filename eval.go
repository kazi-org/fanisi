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
	ClaudeAdmission        *ClaudeAdmission `json:"claude_admission,omitempty"`
	Kazi                   *KaziEvaluation  `json:"kazi,omitempty"`
	ClaudeProvider         string           `json:"claude_provider,omitempty"`
	ClaudeMaxOutputTokens  int              `json:"claude_max_output_tokens,omitempty"`
	RepairFrom             string           `json:"repair_from,omitempty"`
	ClaudeMaxEstimatedCost float64          `json:"claude_max_estimated_cost_usd"`
	SchemaVersion          int              `json:"schema_version"`
	TaskID                 string           `json:"task_id"`
	Repository             string           `json:"repository"`
	Base                   string           `json:"base"`
	WorktreeRoot           string           `json:"worktree_root"`
	Output                 string           `json:"output"`
	TaskConfig             string           `json:"task_config"`
	ProtectedFiles         []string         `json:"protected_files"`
	PreparationSeconds     *float64         `json:"preparation_seconds"`
}

type Attempt struct {
	ControllerArtifacts map[string]ControllerArtifact `json:"controller_artifacts,omitempty"`
	RepairFrom          string                        `json:"repair_from,omitempty"`
	SchemaVersion       int                           `json:"schema_version"`
	TaskID              string                        `json:"task_id"`
	Arm                 string                        `json:"arm"`
	Number              int                           `json:"attempt"`
	Base                string                        `json:"base"`
	Branch              string                        `json:"branch"`
	Workspace           string                        `json:"workspace"`
	StartedAt           time.Time                     `json:"started_at"`
	WallSeconds         float64                       `json:"wall_seconds"`
	PreparationSeconds  *float64                      `json:"preparation_seconds"`
	ExecutionSeconds    float64                       `json:"execution_seconds"`
	VerificationSeconds float64                       `json:"verification_seconds"`
	Status              string                        `json:"status"`
	Error               string                        `json:"error,omitempty"`
	PatchSHA            string                        `json:"patch_sha256"`
	ScopeOK             bool                          `json:"scope_ok"`
	VerificationPassed  bool                          `json:"verification_passed"`
	BaselineExit        int                           `json:"baseline_exit"`
	ConfigurationSHA    string                        `json:"configuration_sha256"`
	PromptSHA           string                        `json:"prompt_sha256"`
	Protected           map[string]string             `json:"protected_files"`
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
	if e.ClaudeProvider != "" && e.ClaudeProvider != "Z.AI" {
		return e, Config{}, errors.New("Claude provider must be empty or Z.AI")
	}
	if e.ClaudeAdmission != nil {
		if err := e.ClaudeAdmission.validate(); err != nil {
			return e, Config{}, err
		}
		if e.ClaudeProvider != "Z.AI" {
			return e, Config{}, errors.New("Claude admission requires Z.AI provider routing")
		}
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
	if arm != "fanisi" && arm != "claude" && arm != "claude-packet" && arm != "kazi-claude" {
		return errors.New("arm must be fanisi, claude, claude-packet, or kazi-claude")
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
	if arm == "kazi-claude" {
		if e.Kazi == nil || e.Kazi.MaxDispatches < 1 || e.Kazi.MaxDispatches > 2 || cfg.MaxCalls < e.Kazi.MaxDispatches || !filepath.IsAbs(e.Kazi.Executable) || e.ClaudeProvider != "Z.AI" || len(e.Kazi.ExecutableSHA) != 64 || e.ClaudeMaxOutputTokens < 1024 {
			return errors.New("Kazi arm requires pinned executable, 1..2 dispatches and bounded Claude output")
		}
		if e.ClaudeAdmission != nil && e.ClaudeAdmission.MaxRequests < e.Kazi.MaxDispatches {
			return errors.New("request allowance cannot reserve every Kazi dispatch")
		}
		for path, hash := range e.Kazi.Artifacts {
			if !filepath.IsLocal(path) || filepath.Clean(path) != path || strings.Contains(path, "\\") || len(hash) != 64 {
				return errors.New("controller artifacts require exact local paths and SHA256")
			}
			for _, write := range cfg.WritePaths {
				if path == write {
					return errors.New("controller artifact overlaps candidate scope")
				}
			}
		}
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
	baseline, err := workspaceInventory(cfg.Workspace)
	if err != nil {
		return err
	}
	baselinePath := filepath.Join(output, "workspace-baseline.json")
	if err := writeJSON(baselinePath, baseline); err != nil {
		return err
	}
	baselineRaw, err := os.ReadFile(baselinePath)
	if err != nil {
		return err
	}
	a.Protected[baselinePath] = digest(baselineRaw)
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
	if arm != "claude" && arm != "kazi-claude" {
		prompt, _, err = buildPacket(ctx, cfg)
		if err != nil {
			return err
		}
	}
	a.PromptSHA = digest(prompt)
	if err := writeNew(filepath.Join(output, "submitted-prompt.md"), prompt); err != nil {
		return err
	}
	var kaziExecution KaziExecution
	executionStart := time.Now()
	workerCtx, stopWorker := context.WithTimeout(ctx, time.Duration(cfg.MaxSeconds)*time.Second)
	if arm == "fanisi" {
		runErr = run(workerCtx, cfg)
	} else if arm == "kazi-claude" {
		runErr = runKazi(workerCtx, e, cfg, output, prompt, baseline, &kaziExecution)
	} else {
		claudeCfg := cfg
		claudeCfg.MaxCost = e.ClaudeMaxEstimatedCost
		runErr = runClaude(workerCtx, claudeCfg, output, prompt, ClaudeOptions{MaxOutputTokens: e.ClaudeMaxOutputTokens, Provider: e.ClaudeProvider, Admission: e.ClaudeAdmission})
	}
	stopWorker()
	a.ExecutionSeconds = time.Since(executionStart).Seconds()
	for path, want := range kaziExecution.Protected {
		a.Protected[path] = want
	}
	for path, want := range a.Protected {
		raw, err := os.ReadFile(path)
		if err != nil || digest(raw) != want {
			return errors.Join(runErr, fmt.Errorf("protected input changed before final grading: %s", path))
		}
	}
	graderEnv := map[string]string{}
	if arm == "kazi-claude" {
		a.ControllerArtifacts = kaziExecution.Artifacts
		manifestPath := filepath.Join(output, "controller-artifacts.json")
		raw, readErr := os.ReadFile(manifestPath)
		if readErr != nil || digest(raw) != kaziExecution.ArtifactManifestSHA {
			return errors.Join(runErr, errors.New("parent artifact manifest integrity failure"))
		}
		if kaziExecution.Manifest.IntegrityError != "" {
			return errors.Join(runErr, errors.New(kaziExecution.Manifest.IntegrityError))
		}
		for _, name := range []string{"dispatch-manifest.json", "controller-artifacts.json"} {
			raw, err := os.ReadFile(filepath.Join(output, name))
			if err != nil {
				return errors.Join(runErr, err)
			}
			a.Protected[filepath.Join(output, name)] = digest(raw)
		}
		graderEnv["FANISI_CONTROLLER_ARTIFACT_MANIFEST"] = manifestPath
	}
	if err := auditWorkspace(ctx, cfg, sha, baseline, a.ControllerArtifacts); err != nil {
		return errors.Join(runErr, err)
	}
	checkStart := time.Now()
	// Run independently even after a worker failure; capture useful partial candidates.
	_, exit, verifyErr := runCommandWithEnv(ctx, cfg.Workspace, cfg.VerifyCommand, filepath.Join(output, "verification.log"), graderEnv)
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
	err = auditWorkspace(ctx, cfg, sha, baseline, a.ControllerArtifacts)
	a.ScopeOK = err == nil
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
	temporary, err := os.MkdirTemp("", "fanisi-candidate-index-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = cfg.Workspace
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(temporary, "index"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("candidate index %s: %w: %s", args[0], err, out)
		}
		return out, nil
	}
	if _, err := run("read-tree", "HEAD"); err != nil {
		return nil, err
	}
	for _, path := range cfg.WritePaths {
		info, err := os.Lstat(filepath.Join(cfg.Workspace, path))
		if errors.Is(err, os.ErrNotExist) {
			if _, err := run("update-index", "--force-remove", "--", path); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("candidate path is not regular: %s", path)
		}
		object, err := run("hash-object", "-w", "--no-filters", "--", path)
		if err != nil {
			return nil, err
		}
		mode := "100644"
		if info.Mode().Perm()&0111 != 0 {
			mode = "100755"
		}
		if _, err := run("update-index", "--add", "--cacheinfo", mode, strings.TrimSpace(string(object)), path); err != nil {
			return nil, err
		}
	}
	return run("diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv", "HEAD")
}

// Recheck the whole workspace, including new files, at verification and review.
func scopeUnchanged(ctx context.Context, cfg Config, sha string) (bool, error) {
	return scopeUnchangedWithArtifacts(ctx, cfg, sha, nil)
}

func scopeUnchangedWithArtifacts(ctx context.Context, cfg Config, sha string, artifacts map[string]ControllerArtifact) (bool, error) {
	changedPaths, err := gitOutput(ctx, cfg.Workspace, "diff", "--name-only", "-z", sha)
	if err != nil {
		return false, err
	}
	untracked, err := gitOutput(ctx, cfg.Workspace, "ls-files", "--others", "-z")
	if err != nil {
		return false, err
	}
	scopeOK := true
	for path, want := range artifacts {
		identity, err := fileIdentity(filepath.Join(cfg.Workspace, path))
		if err != nil || identity != want.FileIdentity {
			return false, errors.New("controller artifact changed after verification")
		}
	}
	allowed := map[string]bool{}
	for _, p := range cfg.WritePaths {
		allowed[p] = true
	}
	for _, p := range strings.Split(string(append(changedPaths, untracked...)), "\x00") {
		if p != "" && !allowed[p] && artifacts[p].SHA256 == "" {
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
