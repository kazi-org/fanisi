package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tokenEvent(stamp, counters string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s}}}`+"\n", stamp, counters)
}

const usageBefore = `{"input_tokens":100,"cached_input_tokens":60,"output_tokens":10,"reasoning_output_tokens":4,"total_tokens":99999}`
const usageAfter = `{"input_tokens":180,"cached_input_tokens":110,"output_tokens":30,"reasoning_output_tokens":9,"total_tokens":99999}`

func TestCoordinatorWindowUsesComponentsAndTimestampInstants(t *testing.T) {
	t.Parallel()
	// Fractional timestamps sort incorrectly against the following plain-Z form
	// under lexical comparison. The command must compare parsed instants.
	input := tokenEvent("2026-09-07T00:38:46.999Z", usageBefore) +
		`{"timestamp":"2026-09-07T00:38:47Z","type":"response_item","payload":{"secret":"PRIVATE CONTENT"}}` + "\n" +
		tokenEvent("2026-09-06T17:38:47.500-07:00", usageAfter) +
		tokenEvent("2026-09-07T01:00:01Z", `{"input_tokens":900,"cached_input_tokens":700,"output_tokens":500,"reasoning_output_tokens":100}`)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 7, 0, 38, 47, 0, time.UTC)
	var out bytes.Buffer
	if err := coordinatorUsage(context.Background(), path, from, from.Add(time.Minute), &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tokens coordinatorTokens `json:"tokens"`
		Cost   *float64          `json:"cost_usd"`
		SHA    string            `json:"snapshot_sha256"`
		Before time.Time         `json:"observed_before"`
		End    time.Time         `json:"observed_end"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := coordinatorTokens{Input: 80, Cached: 50, Output: 20, Reasoning: 5, Total: 100}
	sum := sha256.Sum256([]byte(input))
	if got.Tokens != want || got.Cost != nil || got.SHA != hex.EncodeToString(sum[:]) || !got.Before.Equal(from.Add(-time.Millisecond)) || !got.End.Equal(from.Add(500*time.Millisecond)) {
		t.Fatalf("incorrect accounting: %+v", got)
	}
	if strings.Contains(out.String(), "PRIVATE CONTENT") || strings.Contains(out.String(), path) {
		t.Fatal("transcript content or path leaked")
	}
}

func TestCoordinatorUsageRefusesUnreliableWindows(t *testing.T) {
	t.Parallel()
	before := tokenEvent("2026-09-07T00:00:00Z", usageBefore)
	after := tokenEvent("2026-09-07T00:02:00Z", usageAfter)
	for _, tc := range []struct{ name, input string }{
		{"no baseline", after}, {"no end observation", before},
		{"missing counter", before + tokenEvent("2026-09-07T00:02:00Z", `{"input_tokens":180,"output_tokens":30}`)},
		{"null counter", before + tokenEvent("2026-09-07T00:02:00Z", `{"input_tokens":null,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0}`)},
		{"counter reset", before + after + tokenEvent("2026-09-07T00:03:00Z", usageBefore)},
		{"out of order", before + after + tokenEvent("2026-09-07T00:01:30Z", usageAfter)},
		{"truncated JSON", before + after + `{"unfinished":`},
		{"oversized line", before + after + strings.Repeat("x", 17<<20)},
		{"cached exceeds input", before + tokenEvent("2026-09-07T00:02:00Z", `{"input_tokens":180,"cached_input_tokens":181,"output_tokens":30,"reasoning_output_tokens":9}`)},
		{"reasoning exceeds output", before + tokenEvent("2026-09-07T00:02:00Z", `{"input_tokens":180,"cached_input_tokens":110,"output_tokens":30,"reasoning_output_tokens":31}`)},
		{"sum overflow", before + tokenEvent("2026-09-07T00:02:00Z", `{"input_tokens":9223372036854775807,"cached_input_tokens":110,"output_tokens":30,"reasoning_output_tokens":9}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.jsonl")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			from := time.Date(2026, 9, 7, 0, 1, 0, 0, time.UTC)
			if err := coordinatorUsage(context.Background(), path, from, from.Add(3*time.Minute), &out); err == nil || out.Len() != 0 {
				t.Fatalf("unreliable usage accepted: err=%v output=%s", err, out.String())
			}
		})
	}
}

func TestCoordinatorUsageCLIRejectsMissingWindow(t *testing.T) {
	if err := mainContext(context.Background(), []string{"coordinator-usage", "trace.jsonl"}); err == nil {
		t.Fatal("missing timestamps accepted")
	}
}
