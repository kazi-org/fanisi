package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

type Receipt struct {
	ID        string   `json:"id"`
	Model     string   `json:"model"`
	Provider  string   `json:"provider_name"`
	Cost      *float64 `json:"total_cost"`
	Prompt    *int     `json:"native_tokens_prompt"`
	Output    *int     `json:"native_tokens_completion"`
	Cached    *int     `json:"native_tokens_cached"`
	Reasoning *int     `json:"native_tokens_reasoning"`
}

type ReceiptLedger struct {
	SchemaVersion int       `json:"schema_version"`
	Generations   []Receipt `json:"generations"`
	Unresolved    []string  `json:"unresolved"`
	Complete      bool      `json:"complete"`
	KnownCost     float64   `json:"known_cost_usd"`
	KnownTokens   int       `json:"known_tokens"`
	Cost          *float64  `json:"cost_usd"`
	Tokens        *int      `json:"tokens"`
	RetrievedAt   time.Time `json:"retrieved_at"`
}

func generationIDs(dir string) ([]string, error) {
	seen := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "run", "call-*-response.json"))
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		var r struct {
			ID string `json:"id"`
		}
		if err := readJSON(p, &r); err != nil {
			return nil, err
		}
		if r.ID != "" {
			seen[r.ID] = true
		}
	}
	relayFiles, err := filepath.Glob(filepath.Join(dir, "relay-request-*.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range relayFiles {
		var record struct {
			IDs      []string `json:"generation_ids"`
			Complete bool     `json:"stream_complete"`
			Gap      bool     `json:"identity_gap"`
		}
		if err := readJSON(path, &record); err != nil {
			return nil, err
		}
		for _, id := range record.IDs {
			if id != "" {
				seen[id] = true
			}
		}
	}
	f, err := os.Open(filepath.Join(dir, "claude-stream.jsonl"))
	if err == nil {
		defer f.Close()
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 65536), 16*1024*1024)
		for scan.Scan() {
			var r TraceEvent
			if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
				continue
			}
			if r.Type == "assistant" && r.Message.ID != "" {
				seen[r.Message.ID] = true
			}
		}
		if err := scan.Err(); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, errors.New("no generation IDs found; spend is unknown")
	}
	return ids, nil
}

func reconcile(ctx context.Context, dir, keyPath string) error {
	key, err := resolveKey(keyPath)
	if err != nil {
		return err
	}
	return reconcileWithClient(ctx, dir, key, &http.Client{Timeout: 30 * time.Second})
}

func reconcileWithClient(ctx context.Context, dir, key string, client *http.Client) error {
	ids, err := generationIDs(dir)
	if err != nil {
		return err
	}
	ledger := ReceiptLedger{SchemaVersion: 1, Generations: []Receipt{}, Unresolved: []string{}, RetrievedAt: time.Now().UTC()}
	var failures []error
	for _, id := range ids {
		r, err := fetchReceipt(ctx, client, key, id)
		if err != nil {
			ledger.Unresolved = append(ledger.Unresolved, id)
			failures = append(failures, fmt.Errorf("generation %s: %w", id, err))
			continue
		}
		ledger.Generations = append(ledger.Generations, r)
		ledger.KnownCost += *r.Cost
		ledger.KnownTokens += *r.Prompt + *r.Output
	}
	// For Fanisi, recorded attempted calls must all resolve. A failed HTTP call
	// can have no generation ID; a set of available receipts is not full coverage.
	var runResult struct {
		Calls int `json:"calls"`
	}
	resultPath := filepath.Join(dir, "run", "result.json")
	if err := readJSON(resultPath, &runResult); err == nil {
		if runResult.Calls != len(ids) {
			ledger.Unresolved = append(ledger.Unresolved, "attempted call without unique generation id")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Claude's stream has no API-ingress counter. State that coverage limit rather
	// than inferring that invisible background or interrupted requests were free.
	if _, err := os.Stat(filepath.Join(dir, "claude-stream.jsonl")); err == nil {
		var terminal struct {
			Subtype string `json:"subtype"`
			IsError bool   `json:"is_error"`
		}
		found, err := claudeTerminal(filepath.Join(dir, "claude-stream.jsonl"), &terminal)
		if err != nil || !found || terminal.IsError {
			ledger.Unresolved = append(ledger.Unresolved, "Claude did not emit a successful terminal result; requests without IDs may be unmetered")
		}
	}
	relayFiles, err := filepath.Glob(filepath.Join(dir, "relay-request-*.json"))
	if err != nil {
		return err
	}
	for _, path := range relayFiles {
		var record struct {
			IDs      []string `json:"generation_ids"`
			Complete bool     `json:"stream_complete"`
			Gap      bool     `json:"identity_gap"`
		}
		if err := readJSON(path, &record); err != nil {
			return err
		}
		if len(record.IDs) == 0 || slices.Contains(record.IDs, "") {
			ledger.Unresolved = append(ledger.Unresolved, filepath.Base(path)+": upstream request has no observed generation id")
		}
		if record.Gap {
			ledger.Unresolved = append(ledger.Unresolved, filepath.Base(path)+": stream included an unrecognized generation identity")
		}
		if !record.Complete {
			ledger.Unresolved = append(ledger.Unresolved, filepath.Base(path)+": upstream stream did not finish")
		}
	}
	ledger.Complete = len(ledger.Unresolved) == 0
	if ledger.Complete {
		ledger.Cost = &ledger.KnownCost
		ledger.Tokens = &ledger.KnownTokens
	}
	if err := writeJSON(filepath.Join(dir, "provider-ledger.json"), ledger); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(ledger); err != nil {
		return err
	}
	if !ledger.Complete {
		failures = append(failures, errors.New("receipt coverage incomplete; totals remain unknown"))
	}
	return errors.Join(failures...)
}

func fetchReceipt(ctx context.Context, client *http.Client, key, id string) (Receipt, error) {
	var r Receipt
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openrouter.ai/api/v1/generation?id="+url.QueryEscape(id), nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(req)
	if err != nil {
		return r, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return r, fmt.Errorf("receipt lookup HTTP %d", response.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return r, err
	}
	var envelope struct {
		Data Receipt `json:"data"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return r, err
	}
	r = envelope.Data
	if r.ID != id || !validModel(r.Model) || r.Cost == nil || *r.Cost < 0 || r.Prompt == nil || *r.Prompt < 0 || r.Output == nil || *r.Output < 0 {
		return r, errors.New("receipt lacks matching model/id or complete nonnegative native accounting")
	}
	for _, pair := range []struct {
		part  *int
		total int
	}{{r.Cached, *r.Prompt}, {r.Reasoning, *r.Output}} {
		if pair.part != nil && (*pair.part < 0 || *pair.part > pair.total) {
			return r, errors.New("receipt token subset exceeds its total")
		}
	}
	return r, nil
}

func claudeTerminal(path string, out any) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 65536), 16*1024*1024)
	count := 0
	for scan.Scan() {
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(scan.Bytes(), &e) == nil && e.Type == "result" {
			count++
			if err := json.Unmarshal(scan.Bytes(), out); err != nil {
				return false, err
			}
		}
	}
	if err := scan.Err(); err != nil {
		return false, err
	}
	if count > 1 {
		return false, errors.New("duplicate Claude terminal results")
	}
	return count == 1, nil
}

// ReceiptCoverage describes the boundary measured by a provider ledger.
func receiptCoverage(arm string) string {
	if strings.HasPrefix(arm, "claude") {
		return "visible generation IDs; Claude background/transport failures without IDs are not observable"
	}
	return "recorded API calls matched to unique provider generation receipts"
}
