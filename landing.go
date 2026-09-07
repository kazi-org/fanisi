package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Landing struct {
	IndependentReview bool      `json:"independent_review"`
	TargetRef         string    `json:"target_ref"`
	SchemaVersion     int       `json:"schema_version"`
	ID                string    `json:"id"`
	Repository        string    `json:"repository"`
	TargetBase        string    `json:"target_base"`
	Merge             string    `json:"merge_commit"`
	Review            string    `json:"review_record"`
	Evidence          string    `json:"merge_evidence"`
	Revokes           string    `json:"revokes,omitempty"`
	At                time.Time `json:"at"`
	Patch             string    `json:"patch_sha256"`
}

func verifyLanding(ctx context.Context, dir string, l Landing) error {
	if l.SchemaVersion != 1 || !simpleID(l.ID) || l.Evidence == "" {
		return errors.New("landing requires version, identity and merge evidence")
	}
	if l.Revokes != "" {
		if !simpleID(l.Revokes) {
			return errors.New("invalid revoked identity")
		}
		var old Landing
		return readJSON(filepath.Join(dir, "landing-"+l.Revokes+".json"), &old)
	}
	var a Attempt
	if err := readJSON(filepath.Join(dir, "attempt.json"), &a); err != nil {
		return err
	}
	if !a.ScopeOK || !a.VerificationPassed || l.Patch != a.PatchSHA {
		return errors.New("landing requires verified candidate")
	}
	if filepath.Base(l.Review) != l.Review || !strings.HasPrefix(l.Review, "review-") {
		return errors.New("invalid review record")
	}
	var r Review
	if err := readJSON(filepath.Join(dir, l.Review), &r); err != nil {
		return err
	}
	if !l.IndependentReview || strings.TrimSpace(r.Reviewer) == "" || r.Decision != "accept" || r.PatchSHA != a.PatchSHA || (r.Kind != "human" && r.Kind != "agent") {
		return errors.New("landing requires independent accepted review")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "configuration.json"))
	if err != nil {
		return err
	}
	if digest(raw) != a.ConfigurationSHA {
		return errors.New("configuration changed")
	}
	var cfg struct {
		Evaluation Evaluation
		Task       Config
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	canonical := func(repo string) (string, error) {
		b, err := gitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		return strings.TrimSpace(string(b)), err
	}
	want, err := canonical(cfg.Evaluation.Repository)
	if err != nil {
		return err
	}
	got, err := canonical(l.Repository)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("wrong landing repository")
	}
	for _, ref := range []string{l.TargetBase, l.Merge, a.Base} {
		if len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "" {
			return errors.New("landing requires full commit identities")
		}
	}

	if !strings.HasPrefix(l.TargetRef, "refs/heads/") && !strings.HasPrefix(l.TargetRef, "refs/remotes/") {
		return errors.New("landing requires recorded target branch ref")
	}
	if _, err := gitOutput(ctx, l.Repository, "merge-base", "--is-ancestor", l.TargetBase, l.Merge); err != nil {
		return errors.New("merge result is not based on target base")
	}
	if _, err := gitOutput(ctx, l.Repository, "merge-base", "--is-ancestor", l.Merge, l.TargetRef); err != nil {
		return errors.New("merge result is not on recorded target branch")
	}
	patch, err := os.ReadFile(filepath.Join(dir, "candidate.patch"))
	if err != nil {
		return err
	}
	if digest(patch) != a.PatchSHA {
		return errors.New("candidate changed after review")
	}
	tmp, err := os.MkdirTemp("", "fanisi-landing-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	run := func(input []byte, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = l.Repository
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(tmp, "index"))
		cmd.Stdin = bytes.NewReader(input)
		b, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("landing git %s: %w: %s", args[0], err, b)
		}
		return b, nil
	}
	if _, err := run(nil, "read-tree", a.Base); err != nil {
		return err
	}
	if _, err := run(patch, "apply", "--cached", "--binary", "-"); err != nil {
		return err
	}
	tree, err := run(nil, "write-tree")
	if err != nil {
		return err
	}
	// Compare blob identities and modes, excluding unrelated intervening paths.
	// Changes to the reviewed files' baseline remain ambiguous and are rejected.
	delta := func(base, end string) ([]byte, error) {
		args := []string{"diff", "--raw", "--no-abbrev", "--no-renames", base, end, "--"}
		args = append(args, cfg.Task.WritePaths...)
		return run(nil, args...)
	}
	if len(cfg.Task.WritePaths) == 0 {
		return errors.New("missing reviewed scope")
	}
	reviewed, err := delta(a.Base, strings.TrimSpace(string(tree)))
	if err != nil {
		return err
	}
	landed, err := delta(l.TargetBase, l.Merge)
	if err != nil {
		return err
	}
	if len(reviewed) == 0 || !bytes.Equal(reviewed, landed) {
		return errors.New("landed content differs or is ambiguous; re-verify and review")
	}
	return nil
}

func importLanding(ctx context.Context, dir, file string) error {
	var l Landing
	if err := readStrictJSON(file, &l); err != nil {
		return err
	}
	if err := verifyLanding(ctx, dir, l); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	path := filepath.Join(dir, "landing-"+l.ID+".json")
	old, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(old, b) {
			return nil
		}
		return errors.New("conflicting landing identity")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeNew(path, b)
}
