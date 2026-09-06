package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAttributionDeduplicatesPartialAssistantMessages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stream := filepath.Join(dir, "stream.jsonl")
	ledger := filepath.Join(dir, "ledger.json")
	text := `{"type":"assistant","message":{"id":"gen-1","content":[]}}
{"type":"assistant","message":{"id":"gen-1","content":[{"type":"tool_use","id":"tool-1","name":"Read","input":{"path":"file"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"abc"}]}}
{"type":"assistant","message":{"id":"gen-2","content":[]}}
`
	if err := os.WriteFile(stream, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(ledger, map[string]any{"generations": []Generation{
		{ID: "gen-1", Prompt: 100, Cached: 50, Completion: 10, Reasoning: 2, Cost: 0.1},
		{ID: "gen-2", Prompt: 200, Cached: 150, Completion: 30, Reasoning: 10, Cost: 0.2},
	}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := analyzeTo(stream, ledger, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Calls  []Generation `json:"calls"`
		Totals Generation   `json:"totals"`
		Tools  []ToolVolume `json:"tool_result_exposure"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Calls) != 2 || got.Totals.Prompt != 300 || got.Totals.Completion != 40 || got.Totals.Cached != 200 {
		t.Fatalf("overlapping stream usage was double-counted: %+v", got.Totals)
	}
	if len(got.Tools) != 1 || got.Tools[0].ResultBytes != 3 || got.Tools[0].ReplayBytes != 3 {
		t.Fatalf("incorrect replay byte attribution: %+v", got.Tools)
	}
}
