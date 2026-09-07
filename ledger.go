package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Review struct {
	SchemaVersion int       `json:"schema_version"`
	At            time.Time `json:"at"`
	Decision      string    `json:"decision"`
	Reviewer      string    `json:"reviewer"`
	Kind          string    `json:"kind"`
	Seconds       *float64  `json:"seconds"`
	Notes         string    `json:"notes"`
	PatchSHA      string    `json:"patch_sha256"`
}

func recordReview(ctx context.Context, dir, decision, reviewer, kind string, seconds float64, notes string) error {
	if decision != "accept" && decision != "reject" {
		return errors.New("review decision must be accept or reject")
	}
	if strings.TrimSpace(reviewer) == "" || (kind != "human" && kind != "agent") || seconds < -1 || seconds > 86400 || strings.TrimSpace(notes) == "" || len(notes) > 16000 {
		return errors.New("review requires reviewer, human/agent kind, bounded time and substantive notes")
	}
	var a Attempt
	if err := readJSON(filepath.Join(dir, "attempt.json"), &a); err != nil {
		return err
	}
	patch, err := os.ReadFile(filepath.Join(dir, "candidate.patch"))
	if err != nil {
		return err
	}
	if digest(patch) != a.PatchSHA {
		return errors.New("candidate artifact changed since verification")
	}
	if decision == "accept" {
		if a.Status != "verified_pending_review" || !a.ScopeOK || !a.VerificationPassed {
			return errors.New("acceptance requires independent verification and scope checks")
		}
		rawConfig, err := os.ReadFile(filepath.Join(dir, "configuration.json"))
		if err != nil {
			return err
		}
		if digest(rawConfig) != a.ConfigurationSHA {
			return errors.New("evaluation configuration changed since verification")
		}
		var cfg struct{ Task Config }
		if err := readJSON(filepath.Join(dir, "configuration.json"), &cfg); err != nil {
			return err
		}
		if cfg.Task.Workspace != a.Workspace {
			return errors.New("evaluation workspace mismatch")
		}
		ok, err := scopeUnchanged(ctx, cfg.Task, a.Base)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("workspace scope changed after verification")
		}
		current, err := candidatePatch(ctx, cfg.Task)
		if err != nil {
			return err
		}
		if digest(current) != a.PatchSHA {
			return errors.New("worktree changed after verification; re-evaluate before acceptance")
		}
		head, err := gitOutput(ctx, a.Workspace, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(head)) != a.Base {
			return errors.New("worktree HEAD changed after verification")
		}
		for p, want := range a.Protected {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if digest(b) != want {
				return fmt.Errorf("protected acceptance input changed: %s", p)
			}
		}
	}
	var measuredSeconds *float64
	if seconds >= 0 {
		measuredSeconds = &seconds
	}
	r := Review{SchemaVersion: 1, At: time.Now().UTC(), Decision: decision, Reviewer: reviewer, Kind: kind, Seconds: measuredSeconds, Notes: notes, PatchSHA: a.PatchSHA}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("review-%020d.json", r.At.UnixNano()))
	if err := writeNew(path, append(b, '\n')); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(r)
}

type ArmReport struct {
	UntimedReviews          int      `json:"reviews_without_measured_duration"`
	PreparationUnknownTasks int      `json:"tasks_without_measured_preparation"`
	Arm                     string   `json:"arm"`
	Tasks                   int      `json:"tasks_attempted"`
	Attempts                int      `json:"attempts"`
	Accepted                int      `json:"tasks_accepted"`
	HumanAccepted           int      `json:"tasks_human_accepted"`
	AgentAccepted           int      `json:"tasks_agent_accepted"`
	Verified                int      `json:"verified_attempts"`
	KnownCost               float64  `json:"known_provider_cost_usd"`
	KnownTokens             int      `json:"known_provider_tokens"`
	Unresolved              int      `json:"attempts_without_complete_receipts"`
	CostPerAccepted         *float64 `json:"provider_cost_per_accepted_change_usd"`
	TokensPerAccepted       *float64 `json:"provider_tokens_per_accepted_change"`
	ExecutionSeconds        float64  `json:"execution_seconds"`
	PreparationSeconds      float64  `json:"declared_preparation_seconds"`
	AttemptWallSeconds      float64  `json:"summed_attempt_wall_seconds"`
	ReviewSeconds           float64  `json:"recorded_review_seconds"`
	Coverage                string   `json:"receipt_coverage_boundary"`
}

func report(root string, out io.Writer) error {
	paths := []string{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "attempt.json" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("no evaluation attempts found")
	}
	sort.Strings(paths)
	rows := map[string]*ArmReport{}
	tasks := map[string]map[string]bool{}
	accepted := map[string]map[string]string{}
	preparation := map[string]map[string]float64{}
	preparationKnown := map[string]map[string]bool{}
	for _, path := range paths {
		dir := filepath.Dir(path)
		var a Attempt
		if err := readJSON(path, &a); err != nil {
			return err
		}
		if a.SchemaVersion != schemaVersion {
			return errors.New("unsupported attempt schema")
		}
		row := rows[a.Arm]
		if row == nil {
			row = &ArmReport{Arm: a.Arm, Coverage: receiptCoverage(a.Arm)}
			rows[a.Arm] = row
			tasks[a.Arm] = map[string]bool{}
			accepted[a.Arm] = map[string]string{}
			preparation[a.Arm] = map[string]float64{}
			preparationKnown[a.Arm] = map[string]bool{}
		}
		tasks[a.Arm][a.TaskID] = true
		row.Attempts++
		row.ExecutionSeconds += a.ExecutionSeconds
		row.AttemptWallSeconds += a.WallSeconds
		if a.PreparationSeconds != nil {
			preparation[a.Arm][a.TaskID] = max(preparation[a.Arm][a.TaskID], *a.PreparationSeconds)
			preparationKnown[a.Arm][a.TaskID] = true
		}
		if a.VerificationPassed && a.ScopeOK {
			row.Verified++
		}
		var ledger ReceiptLedger
		if err := readJSON(filepath.Join(dir, "provider-ledger.json"), &ledger); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			row.Unresolved++
		} else {
			row.KnownCost += ledger.KnownCost
			row.KnownTokens += ledger.KnownTokens
			if !ledger.Complete {
				row.Unresolved++
			}
		}
		reviews, err := filepath.Glob(filepath.Join(dir, "review-*.json"))
		if err != nil {
			return err
		}
		sort.Strings(reviews)
		var latest Review
		for _, p := range reviews {
			var r Review
			if err := readJSON(p, &r); err != nil {
				return err
			}
			if r.Seconds != nil {
				row.ReviewSeconds += *r.Seconds
			} else {
				row.UntimedReviews++
			}
			latest = r
		}
		if latest.Decision == "accept" && latest.PatchSHA == a.PatchSHA && a.VerificationPassed && a.ScopeOK {
			// Count each task once per arm, regardless of retries or multiple reviews.
			if accepted[a.Arm][a.TaskID] != "human" {
				accepted[a.Arm][a.TaskID] = latest.Kind
			}
		}
	}
	delivery, err := deliveryReport(root, paths)
	if err != nil {
		return err
	}
	effort, err := effortReport(root)
	if err != nil {
		return err
	}
	result := []ArmReport{}
	for arm, row := range rows {
		row.Tasks = len(tasks[arm])
		row.PreparationUnknownTasks = row.Tasks - len(preparationKnown[arm])
		row.Accepted = len(accepted[arm])
		for _, kind := range accepted[arm] {
			if kind == "human" {
				row.HumanAccepted++
			} else {
				row.AgentAccepted++
			}
		}
		for _, seconds := range preparation[arm] {
			row.PreparationSeconds += seconds
		}
		if row.Accepted > 0 && row.Unresolved == 0 {
			cost := row.KnownCost / float64(row.Accepted)
			tokens := float64(row.KnownTokens) / float64(row.Accepted)
			row.CostPerAccepted = &cost
			row.TokensPerAccepted = &tokens
		}
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Arm < result[j].Arm })
	return json.NewEncoder(out).Encode(map[string]any{"schema_version": 1, "arms": result, "effort": effort, "delivery": delivery, "notes": []string{"All recorded attempts, including failures, contribute to spend and runtime. Missing receipts keep per-accepted costs unknown.", "Human and agent acceptance are separate. Acceptance is historical for the verified patch, not a claim that it was merged or deployed.", "Summed attempt wall time is not study elapsed time when attempts overlap. Preparation is declared once per task per arm; review effort is only what reviewers recorded.", "Imported effort appears separately with unknown prices preserved. Provider costs cover the stated receipt boundary, not total project cost."}})
}
