// Fanisi is a bounded coding harness with explicit verification and accounting.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const model = "z-ai/glm-5.3-flash"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mainContext(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Release packaging sets version with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const usageText = `fanisi - measured software changes

  fanisi run --config task.json [--dry-run]
  fanisi packet --config task.json --out packet.md
  fanisi profile RUN_DIRECTORY
  fanisi billing --key-file .env RUN_DIRECTORY
  fanisi analyze CLAUDE_STREAM_JSONL PROVIDER_LEDGER
  fanisi eval --config evaluation.json --arm fanisi|claude|claude-packet [--attempt 1]
  fanisi reconcile --key-file .env ATTEMPT_DIRECTORY
  fanisi review --attempt DIR --decision accept|reject --reviewer NAME --kind human|agent --seconds N --notes-file FILE
  fanisi import-effort STUDY_DIRECTORY RECORD_JSON
  fanisi import-coordinator STUDY_DIRECTORY ATTRIBUTION_JSON COORDINATOR_JSON
  fanisi import-landing ATTEMPT_DIRECTORY RECORD_JSON
  fanisi report STUDY_DIRECTORY
  fanisi coordinator-usage --from RFC3339 --to RFC3339 TRANSCRIPT_JSONL
  fanisi version

Relative config paths resolve beside task.json. Read/write paths resolve in its
workspace. run uses only OpenRouter z-ai/glm-5.3-flash via Z.AI. --dry-run makes
no API or verification calls and writes no run artifacts.
`

func mainContext(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usageText)
		return nil
	}
	switch args[0] {
	case "version", "--version":
		fmt.Println("fanisi", version)
		return nil
	case "claude-bridge":
		fs := flag.NewFlagSet("claude-bridge", flag.ContinueOnError)
		task := fs.String("config", "", "frozen task configuration")
		output := fs.String("output", "", "private dispatch artifact directory")
		cap := fs.Int("max-output-tokens", 0, "explicit per-response output cap")
		cost := fs.Float64("max-estimated-cost", 0, "explicit CLI-estimated per-dispatch cost cap")
		provider := fs.String("provider", "", "optional Z.AI provider pin through a private relay")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *task == "" || *output == "" {
			return errors.New("claude-bridge requires --config and --output")
		}
		return claudeBridge(ctx, *task, *output, *cap, *cost, *provider, fs.Args())
	case "eval":
		fs := flag.NewFlagSet("eval", flag.ContinueOnError)
		path := fs.String("config", "", "frozen evaluation configuration")
		arm := fs.String("arm", "", "fanisi, claude, or claude-packet")
		attempt := fs.Int("attempt", 1, "unique attempt number")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if *path == "" || fs.NArg() != 0 {
			return errors.New("eval requires --config, --arm and no positional arguments")
		}
		return evalRun(ctx, *path, *arm, *attempt)
	case "review":
		fs := flag.NewFlagSet("review", flag.ContinueOnError)
		dir := fs.String("attempt", "", "evaluation attempt directory")
		decision := fs.String("decision", "", "accept or reject")
		reviewer := fs.String("reviewer", "", "reviewer identity")
		kind := fs.String("kind", "", "human or agent")
		seconds := fs.Float64("seconds", -1, "actual review time in seconds; omit if unmeasured")
		notesFile := fs.String("notes-file", "", "review findings")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if *dir == "" || *notesFile == "" || fs.NArg() != 0 {
			return errors.New("review requires --attempt, --notes-file and no positional arguments")
		}
		notes, err := os.ReadFile(*notesFile)
		if err != nil {
			return err
		}
		return recordReview(ctx, *dir, *decision, *reviewer, *kind, *seconds, string(notes))
	case "import-landing":
		if len(args) == 3 {
			return importLanding(ctx, args[1], args[2])
		}
	case "import-coordinator":
		if len(args) == 4 {
			return importEffortSource(args[1], args[2], args[3])
		}
	case "import-effort":
		if len(args) == 3 {
			return importEffort(args[1], args[2])
		}
	case "report":
		if len(args) == 2 {
			return report(args[1], os.Stdout)
		}
	case "coordinator-usage":
		fs := flag.NewFlagSet("coordinator-usage", flag.ContinueOnError)
		start := fs.String("from", "", "start of observation window, RFC3339")
		end := fs.String("to", "", "end of observation window, RFC3339")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("coordinator-usage requires --from, --to and one transcript")
		}
		from, err := time.Parse(time.RFC3339, *start)
		if err != nil {
			return errors.New("--from must be an RFC3339 timestamp")
		}
		to, err := time.Parse(time.RFC3339, *end)
		if err != nil {
			return errors.New("--to must be an RFC3339 timestamp")
		}
		return coordinatorUsage(ctx, fs.Arg(0), from, to, os.Stdout)
	case "profile":
		if len(args) == 2 {
			return profile(args[1])
		}
	case "analyze":
		if len(args) == 3 {
			return analyze(args[1], args[2])
		}
	case "reconcile":
		fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
		key := fs.String("key-file", "", "OpenRouter key file; otherwise environment")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("reconcile requires one attempt directory")
		}
		return reconcile(ctx, fs.Arg(0), *key)
	case "billing":
		fs := flag.NewFlagSet("billing", flag.ContinueOnError)
		key := fs.String("key-file", ".env", "literal OpenRouter key file")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("billing requires one run directory")
		}
		return billing(ctx, fs.Arg(0), *key)
	case "run", "packet":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		path := fs.String("config", "", "task configuration JSON")
		dry := fs.Bool("dry-run", false, "validate task and render packet without API calls")
		out := fs.String("out", "", "packet destination (packet command only)")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if *path == "" || fs.NArg() != 0 {
			return errors.New("--config is required; unexpected positional arguments are refused")
		}
		if args[0] == "run" && *out != "" {
			return errors.New("--out is only supported by packet")
		}
		cfg, err := loadConfig(*path)
		if err != nil {
			return err
		}
		root, err := filepath.EvalSymlinks(cfg.Workspace)
		if err != nil {
			return err
		}
		cfg.Workspace = root
		if *dry || args[0] == "packet" {
			packet, sections, err := buildPacket(ctx, cfg)
			if err != nil {
				return err
			}
			if args[0] == "packet" {
				if *out == "" {
					return errors.New("packet requires --out")
				}
				if err := writeNew(*out, packet); err != nil {
					return err
				}
			}
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"schema_version": schemaVersion, "model": model, "workspace": root, "packet_bytes": len(packet), "packet_sha256": digest(packet), "sections": sections, "dry_run": *dry})
		}
		return run(ctx, cfg)
	}
	return errors.New("unknown command or arguments; run fanisi --help")
}

func readStrictJSON(path string, dest any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%s must contain exactly one JSON object", path)
	}
	return nil
}

func writeNew(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	return errors.Join(err, f.Close())
}

func readJSON(path string, dest any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// dotenv is deliberately a literal reader, never a shell interpreter.
func loadKey(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read key file: %w", err)
	}
	var found string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != "OPENROUTER_API_KEY" {
			continue
		}
		if found != "" {
			return "", errors.New("duplicate OpenRouter key assignment")
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if value == "" || strings.ContainsAny(value, " \t\r\n$`\\\"'") {
			return "", errors.New("key must be a nonempty literal with no expansions")
		}
		found = value
	}
	if found == "" {
		return "", errors.New("OPENROUTER_API_KEY missing")
	}
	return found, nil
}

func splitPath(p string) []string { return strings.Split(p, string(filepath.Separator)) }

func scopedPath(root, name string, allowed []string) (string, error) {
	if err := validRelativePath(name); err != nil {
		return "", err
	}
	ok := false
	for _, p := range allowed {
		if p == name {
			ok = true
		}
	}
	if !ok {
		return "", errors.New("path is outside the declared file scope")
	}
	path := root
	for _, part := range splitPath(name) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlink paths are not allowed")
		}
	}
	return filepath.Join(root, name), nil
}

func resolveKey(path string) (string, error) {
	if path != "" {
		return loadKey(path)
	}
	key := os.Getenv("OPENROUTER_API_KEY")
	if strings.TrimSpace(key) == "" {
		return "", errors.New("set OPENROUTER_API_KEY or configure key_file")
	}
	return key, nil
}

func validModel(value string) bool { return value == model || strings.HasPrefix(value, model+"-") }

func validateUsage(u Usage) error {
	if u.Cost == nil || *u.Cost < 0 || u.Prompt < 0 || u.Completion < 0 || u.Total <= 0 || u.Total != u.Prompt+u.Completion {
		return errors.New("inconsistent or missing provider usage")
	}
	if n := u.PromptDetails.Cached; n != nil && (*n < 0 || *n > u.Prompt) {
		return errors.New("invalid cached input count")
	}
	if n := u.CompletionDetails.Reasoning; n != nil && (*n < 0 || *n > u.Completion) {
		return errors.New("invalid reasoning count")
	}
	return nil
}

func (u *Usage) UnmarshalJSON(b []byte) error {
	type plain Usage
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, key := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
		if len(raw[key]) == 0 || string(raw[key]) == "null" {
			return fmt.Errorf("usage omitted %s", key)
		}
	}
	return json.Unmarshal(b, (*plain)(u))
}
