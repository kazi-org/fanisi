package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

type Generation struct {
	ID         string  `json:"id"`
	Model      string  `json:"model"`
	Prompt     int     `json:"native_tokens_prompt"`
	Completion int     `json:"native_tokens_completion"`
	Cached     int     `json:"native_tokens_cached"`
	Reasoning  int     `json:"native_tokens_reasoning"`
	Cost       float64 `json:"total_cost"`
	Time       float64 `json:"generation_time"`
}

type TraceEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID      string `json:"id"`
		Content []struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			Content json.RawMessage `json:"content"`
			ToolID  string          `json:"tool_use_id"`
		} `json:"content"`
	} `json:"message"`
}

type ToolVolume struct {
	Name            string          `json:"tool"`
	ID              string          `json:"id"`
	Input           json.RawMessage `json:"input"`
	ResultBytes     int             `json:"result_utf8_bytes"`
	SubsequentCalls int             `json:"subsequent_calls"`
	ReplayBytes     int             `json:"potential_replayed_bytes"`
}

func analyze(stream, ledger string) error {
	return analyzeTo(stream, ledger, os.Stdout)
}

func analyzeTo(stream, ledger string, out io.Writer) error {
	var bill struct {
		Generations []Generation `json:"generations"`
	}
	if err := readJSON(ledger, &bill); err != nil {
		return err
	}
	generation := map[string]Generation{}
	for _, g := range bill.Generations {
		generation[g.ID] = g
	}
	f, err := os.Open(stream)
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 65536), 16*1024*1024)
	seen := map[string]bool{}
	tools := map[string]*ToolVolume{}
	resultAt := map[string]int{}
	var calls []Generation
	var unresolved []string
	for scan.Scan() {
		var e TraceEvent
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			continue
		} // stream may contain CLI diagnostics
		if e.Type == "assistant" && !seen[e.Message.ID] {
			seen[e.Message.ID] = true
			if g, ok := generation[e.Message.ID]; ok {
				calls = append(calls, g)
			} else {
				unresolved = append(unresolved, e.Message.ID)
			}
		}
		for _, c := range e.Message.Content {
			if c.Type == "tool_use" {
				tools[c.ID] = &ToolVolume{Name: c.Name, ID: c.ID, Input: c.Input}
			}
			if c.Type == "tool_result" {
				if t, ok := tools[c.ToolID]; ok {
					var s string
					if json.Unmarshal(c.Content, &s) == nil {
						t.ResultBytes = len(s)
					} else {
						t.ResultBytes = len(c.Content)
					}
					resultAt[c.ToolID] = len(seen)
				}
			}
		}
	}
	if err := scan.Err(); err != nil {
		return fmt.Errorf("scan trace: %w", err)
	}
	totals := Generation{}
	for _, c := range calls {
		totals.Prompt += c.Prompt
		totals.Cached += c.Cached
		totals.Completion += c.Completion
		totals.Reasoning += c.Reasoning
		totals.Cost += c.Cost
		totals.Time += c.Time
	}
	volumes := make([]ToolVolume, 0, len(tools))
	for id, t := range tools {
		if at, ok := resultAt[id]; ok {
			t.SubsequentCalls = len(seen) - at
			t.ReplayBytes = t.ResultBytes * t.SubsequentCalls
		}
		volumes = append(volumes, *t)
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].ReplayBytes > volumes[j].ReplayBytes })
	return json.NewEncoder(out).Encode(map[string]any{
		"calls": calls, "totals": totals, "uncached_input_tokens": totals.Prompt - totals.Cached,
		"non_reasoning_output_tokens": totals.Completion - totals.Reasoning,
		"distinct_calls":              len(seen), "unresolved_generations": unresolved, "tool_result_exposure": volumes,
		"attribution_note": "Provider token counts and costs are exact per generation. Tool UTF-8 bytes are measured, not tokens. Potential replay bytes assume each result remains in every later request; Claude's hidden request bodies and compaction are not observable here. Cached input does not prove semantic duplication. No thinking text is included.",
	})
}
