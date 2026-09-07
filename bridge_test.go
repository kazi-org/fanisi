package main

import (
	"testing"
)

func TestBridgePreservesFrozenWorkerLimits(t *testing.T) {
	cfg := Config{Reasoning: "medium", MaxCost: 20, MaxCalls: 15}
	valid := []string{"-p", "real task", "--model", model, "--output-format", "json", "--permission-mode", "dontAsk", "--allowed-tools", "Bash,Read,Edit,Write,Glob,Grep", "--effort", "medium"}
	got, prompt, err := bridgeArguments(cfg, append(append([]string{}, valid...), "--max-budget-usd", "2", "--max-turns", "4"))
	if err != nil || prompt != "real task" || got.MaxCost != 2 || got.MaxCalls != 4 {
		t.Fatalf("controller limit lost: %+v %q %v", got, prompt, err)
	}
	for _, extra := range [][]string{{"--model", "other"}, {"--effort", "low"}, {"--dangerously-skip-permissions", "true"}, {"--max-turns", "0"}, {"--max-budget-usd", "NaN"}, {"--max-budget-usd", "+Inf"}, {"--resume", "session"}, {"--max-turns"}} {
		if _, _, err := bridgeArguments(cfg, append(append([]string{}, valid...), extra...)); err == nil {
			t.Fatalf("accepted unsupported or conflicting args: %v", extra)
		}
	}
	if _, _, err := bridgeArguments(cfg, []string{"-p", "task"}); err == nil {
		t.Fatal("accepted missing model selection")
	}
}

func TestBridgeCannotIncreaseTaskLimits(t *testing.T) {
	cfg := Config{Reasoning: "medium", MaxCost: 2, MaxCalls: 4}
	got, _, err := bridgeArguments(cfg, []string{"-p", "task", "--model", model, "--output-format", "json", "--max-budget-usd", "99", "--max-turns", "90"})
	if err != nil || got.MaxCost != 2 || got.MaxCalls != 4 {
		t.Fatalf("bridge increased budget: %+v %v", got, err)
	}
}

func TestBridgeAcceptsActualKaziToolArgumentShape(t *testing.T) {
	cfg := Config{Reasoning: "medium", MaxCost: 20, MaxCalls: 15}
	args := []string{"-p", "task", "--output-format", "json", "--allowed-tools", "Bash", "Read", "Edit", "Write", "Glob", "Grep", "--permission-mode", "dontAsk", "--model", model, "--tools", "Read", "Edit", "Write", "Bash", "Glob", "Grep", "--strict-mcp-config", "--effort", "medium"}
	if _, prompt, err := bridgeArguments(cfg, args); err != nil || prompt != "task" {
		t.Fatalf("real controller argument shape failed: %q %v", prompt, err)
	}
	args = append(args, "--allowedTools", "Bash")
	if _, _, err := bridgeArguments(cfg, args); err == nil {
		t.Fatal("accepted a duplicate tools option")
	}
}
