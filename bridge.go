package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

// bridgeArguments accepts only the controller options that this measured worker
// contract can preserve. Unknown options fail before any inference request.
func bridgeArguments(cfg Config, args []string) (Config, string, error) {
	seen := map[string]bool{}
	prompt := ""
	for len(args) > 0 {
		if len(args) < 2 {
			return cfg, "", errors.New("bridge requires valued Claude arguments")
		}
		name, value := args[0], args[1]
		args = args[2:]
		if seen[name] {
			return cfg, "", fmt.Errorf("duplicate bridge argument: %s", name)
		}
		seen[name] = true
		switch name {
		case "-p":
			prompt = value
		case "--model":
			if value != model {
				return cfg, "", errors.New("bridge model must be the pinned OpenRouter model")
			}
		case "--output-format":
			if value != "json" {
				return cfg, "", errors.New("bridge expects controller JSON output")
			}
		case "--effort":
			if value != cfg.Reasoning {
				return cfg, "", errors.New("controller effort differs from frozen task")
			}
		case "--permission-mode":
			if value != "dontAsk" {
				return cfg, "", errors.New("bridge requires dontAsk permissions")
			}
		case "--allowedTools":
			if value != "Bash,Read,Edit,Write,Glob,Grep" {
				return cfg, "", errors.New("controller tools differ from frozen trial")
			}
		case "--max-budget-usd":
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
				return cfg, "", errors.New("invalid controller estimated budget")
			}
			cfg.MaxCost = min(cfg.MaxCost, n)
		case "--max-turns":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return cfg, "", errors.New("invalid controller turn budget")
			}
			cfg.MaxCalls = min(cfg.MaxCalls, n)
		default:
			return cfg, "", fmt.Errorf("unsupported controller argument: %s", name)
		}
	}
	if prompt == "" || !seen["--model"] || !seen["--output-format"] {
		return cfg, "", errors.New("bridge requires prompt, pinned model and JSON output")
	}
	return cfg, prompt, nil
}

func claudeBridge(ctx context.Context, task, output string, maxOutput int, estimatedCost float64, args []string) error {
	cfg, err := loadConfig(task)
	if err != nil {
		return err
	}
	if math.IsNaN(estimatedCost) || math.IsInf(estimatedCost, 0) || estimatedCost <= 0 || estimatedCost > 100 || maxOutput < 1024 || maxOutput > 64000 {
		return errors.New("bridge requires explicit bounded output and CLI-estimated cost limits")
	}
	cfg.MaxCost = estimatedCost
	cfg, prompt, err := bridgeArguments(cfg, args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(cfg.Workspace)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("bridge cwd differs from the frozen task workspace")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(output, "dispatch-")
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "controller-request.json"), map[string]any{"argv": args, "task": task, "workspace": cfg.Workspace}); err != nil {
		return err
	}
	runErr := executeClaude(ctx, cfg, dir, []byte(prompt), maxOutput, false)
	var terminal json.RawMessage
	found, parseErr := claudeTerminal(filepath.Join(dir, "claude-stream.jsonl"), &terminal)
	if parseErr != nil || !found {
		return errors.Join(runErr, parseErr, errors.New("bridge has no unique terminal Claude result; retain dispatch logs"))
	}
	if _, err := fmt.Fprintln(os.Stdout, string(terminal)); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}
