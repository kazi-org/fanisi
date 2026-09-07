package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

type coordinatorTokens struct {
	Input     int64 `json:"input_tokens"`
	Cached    int64 `json:"cached_input_tokens"`
	Output    int64 `json:"output_tokens"`
	Reasoning int64 `json:"reasoning_output_tokens"`
	Total     int64 `json:"total_tokens"`
}

type coordinatorObservation struct {
	At     time.Time
	Tokens coordinatorTokens
}

func coordinatorUsage(ctx context.Context, path string, from, to time.Time, out io.Writer) error {
	if from.IsZero() || !to.After(from) {
		return errors.New("coordinator-usage requires a nonempty increasing time window")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening transcript: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspecting transcript: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("transcript must be a regular file")
	}
	// Freeze the byte boundary even when the current session keeps appending.
	hash := sha256.New()
	snapshot := io.NewSectionReader(file, 0, info.Size())
	scanner := bufio.NewScanner(io.TeeReader(snapshot, hash))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	var baseline, last, previous *coordinatorObservation
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var event struct {
			Timestamp time.Time `json:"timestamp"`
			Type      string    `json:"type"`
			Payload   struct {
				Type string `json:"type"`
				Info struct {
					Usage json.RawMessage `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("invalid transcript JSON at line %d", line)
		}
		raw := event.Payload.Info.Usage
		if event.Type != "event_msg" || event.Payload.Type != "token_count" || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if event.Timestamp.IsZero() {
			return fmt.Errorf("missing token observation timestamp at line %d", line)
		}
		if event.Timestamp.After(to) {
			continue
		}
		var usage struct {
			Input     *int64 `json:"input_tokens"`
			Cached    *int64 `json:"cached_input_tokens"`
			Output    *int64 `json:"output_tokens"`
			Reasoning *int64 `json:"reasoning_output_tokens"`
		}
		if err := json.Unmarshal(raw, &usage); err != nil || usage.Input == nil || usage.Cached == nil || usage.Output == nil || usage.Reasoning == nil {
			return fmt.Errorf("incomplete token counters at line %d", line)
		}
		tokens := coordinatorTokens{Input: *usage.Input, Cached: *usage.Cached, Output: *usage.Output, Reasoning: *usage.Reasoning}
		if !validCoordinatorTokens(tokens) {
			return fmt.Errorf("inconsistent token counters at line %d", line)
		}
		current := &coordinatorObservation{At: event.Timestamp, Tokens: tokens}
		if previous != nil && current.At.Before(previous.At) {
			return fmt.Errorf("token observations out of order at line %d", line)
		}
		if current.At.Before(from) {
			baseline = current
			previous = current
			continue
		}
		if previous != nil {
			a, b := current.Tokens, previous.Tokens
			delta := coordinatorTokens{Input: a.Input - b.Input, Cached: a.Cached - b.Cached, Output: a.Output - b.Output, Reasoning: a.Reasoning - b.Reasoning}
			if !validCoordinatorTokens(delta) {
				return fmt.Errorf("token counters reset or changed inconsistently at line %d", line)
			}
		}
		previous = current
		last = current
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading transcript: %w", err)
	}
	readBytes, err := snapshot.Seek(0, io.SeekCurrent)
	if err != nil || readBytes != info.Size() {
		return errors.New("transcript shortened while reading its snapshot")
	}
	if baseline == nil || last == nil {
		return errors.New("window needs a cumulative observation before its start and another within it")
	}
	a, b := last.Tokens, baseline.Tokens
	delta := coordinatorTokens{Input: a.Input - b.Input, Cached: a.Cached - b.Cached, Output: a.Output - b.Output, Reasoning: a.Reasoning - b.Reasoning}
	if !validCoordinatorTokens(delta) {
		return errors.New("inconsistent cumulative window")
	}
	delta.Total = delta.Input + delta.Output
	return json.NewEncoder(out).Encode(struct {
		SchemaVersion  int               `json:"schema_version"`
		Source         string            `json:"source"`
		SnapshotSHA    string            `json:"snapshot_sha256"`
		SnapshotBytes  int64             `json:"snapshot_bytes"`
		From           time.Time         `json:"from"`
		To             time.Time         `json:"to"`
		ObservedBefore time.Time         `json:"observed_before"`
		ObservedEnd    time.Time         `json:"observed_end"`
		Tokens         coordinatorTokens `json:"tokens"`
		CostUSD        *float64          `json:"cost_usd"`
		Notes          []string          `json:"notes"`
	}{1, "codex_cumulative_token_count", hex.EncodeToString(hash.Sum(nil)), info.Size(), from, to, baseline.At, last.At, delta, nil, []string{
		"Cumulative observation delta; boundary requests can overlap the requested window. No usage after observed_end is inferred.",
		"Input includes cached tokens. Reasoning is a subset of output. Total is input plus output; the transcript's total_tokens field is not trusted.",
		"Coordinator usage only. Provider worker receipts, external reviews, and dollar pricing are separate; missing prices are not zero.",
	}})
}

func validCoordinatorTokens(t coordinatorTokens) bool {
	return t.Input >= 0 && t.Cached >= 0 && t.Cached <= t.Input && t.Output >= 0 && t.Reasoning >= 0 && t.Reasoning <= t.Output && t.Input <= math.MaxInt64-t.Output
}
