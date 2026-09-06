package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func profile(dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "call-*-response.json"))
	if err != nil {
		return err
	}
	requests, err := filepath.Glob(filepath.Join(dir, "call-*-request.json"))
	if err != nil {
		return err
	}
	if len(files) == 0 && len(requests) == 0 {
		return errors.New("no API artifacts found")
	}
	roles := map[string]int{}
	tools := map[string]int{}
	providers := map[string]int{}
	var totals struct {
		Prompt    int     `json:"input_tokens_including_cache"`
		Cached    int     `json:"cached_input_tokens"`
		Output    int     `json:"output_tokens"`
		Reasoning int     `json:"reasoning_tokens"`
		Cost      float64 `json:"cost_usd"`
	}
	seen := map[string]bool{}
	unresolved := []string{}
	cacheComplete, reasoningComplete := true, true
	for _, path := range files {
		var r Reply
		if err := readJSON(path, &r); err != nil {
			unresolved = append(unresolved, filepath.Base(path))
			continue
		}
		if r.ID == "" || validateUsage(r.Usage) != nil {
			unresolved = append(unresolved, filepath.Base(path))
			continue
		}
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true

		totals.Prompt += r.Usage.Prompt
		if n := r.Usage.PromptDetails.Cached; n != nil {
			totals.Cached += *n
		} else {
			cacheComplete = false
		}
		totals.Output += r.Usage.Completion
		if n := r.Usage.CompletionDetails.Reasoning; n != nil {
			totals.Reasoning += *n
		} else {
			reasoningComplete = false
		}
		totals.Cost += *r.Usage.Cost
		providers[r.Provider]++
		for _, c := range r.Choices {
			b, err := json.Marshal(c.Message["tool_calls"])
			if err != nil {
				return err
			}
			var calls []Call
			if err := json.Unmarshal(b, &calls); err != nil {
				return err
			}
			for _, call := range calls {
				tools[call.Function.Name]++
			}
		}
	}
	for _, path := range requests {
		responsePath := strings.TrimSuffix(path, "-request.json") + "-response.json"
		if _, err := os.Stat(responsePath); errors.Is(err, os.ErrNotExist) {
			unresolved = append(unresolved, filepath.Base(responsePath))
		} else if err != nil {
			return err
		}
		var req struct {
			Messages []json.RawMessage `json:"messages"`
			Tools    json.RawMessage   `json:"tools"`
		}
		if err := readJSON(path, &req); err != nil {
			return err
		}
		roles["tool_schema"] += len(req.Tools)
		for _, raw := range req.Messages {
			var m struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			roles[m.Role] += len(raw)
		}
	}
	return writeJSON(filepath.Join(dir, "profile.json"), map[string]any{"calls": len(seen), "response_files": len(files), "unresolved_responses": unresolved, "usage_complete": len(unresolved) == 0, "cached_counts_complete": cacheComplete, "reasoning_counts_complete": reasoningComplete, "usage_known_subtotals": totals, "providers": providers, "tool_calls": tools,
		"cumulative_request_component_bytes": roles, "note": "Totals are known subtotals; missing usage is explicitly unresolved, never a zero-cost request. Optional cache/reasoning counts carry separate coverage. Usage is provider-native tokens. Component sizes are exact serialized JSON bytes over all recorded requests, not token attribution. Repeated roles include cached context. Reasoning details are preserved in assistant history. Includes failed attempts with a returned API response; a missing/failed HTTP response remains unmetered until provider reconciliation."})
}

func billing(ctx context.Context, dir, keyPath string) error {
	key, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(dir, "call-*-response.json"))
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	rows := []map[string]any{}
	seen := map[string]bool{}
	for _, path := range files {
		// Receipt reconciliation must also work when response usage is incomplete.
		var reply struct {
			ID string `json:"id"`
		}
		if err := readJSON(path, &reply); err != nil {
			return err
		}
		if reply.ID == "" {
			return errors.New("response has no generation id; attribution unresolved")
		}
		if seen[reply.ID] {
			continue
		}
		seen[reply.ID] = true
		req, err := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/generation?id="+url.QueryEscape(reply.ID), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		closeErr := resp.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("generation lookup HTTP %d; reconciliation incomplete", resp.StatusCode)
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(b, &envelope); err != nil {
			return err
		}
		if envelope.Data["id"] != reply.ID {
			return errors.New("receipt generation id mismatch; reconciliation incomplete")
		}
		row := map[string]any{}
		for _, key := range []string{"id", "model", "provider_name", "total_cost", "native_tokens_prompt", "native_tokens_completion", "native_tokens_cached", "native_tokens_reasoning", "latency", "generation_time"} {
			row[key] = envelope.Data[key]
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return errors.New("no generations to reconcile")
	}
	if err := writeJSON(filepath.Join(dir, "provider-ledger.json"), map[string]any{"generations": rows}); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Recorded", len(rows), "provider generation receipts")
	return nil
}
