package main

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"sync"
)

// ClaudeAdmission bounds requests and declared routing prices, not invoice cost.
type ClaudeAdmission struct {
	MaxRequests     int            `json:"max_requests"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	MaxPrice        AdmissionPrice `json:"max_price_usd_per_million"`
}

type AdmissionPrice struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
}

func (a ClaudeAdmission) validate() error {
	if a.MaxRequests < 1 || a.MaxOutputTokens < 1 || a.MaxOutputTokens > 64000 || a.MaxPrice.Prompt <= 0 || a.MaxPrice.Completion <= 0 || math.IsInf(a.MaxPrice.Prompt, 0) || math.IsInf(a.MaxPrice.Completion, 0) || math.IsNaN(a.MaxPrice.Prompt) || math.IsNaN(a.MaxPrice.Completion) {
		return errors.New("Claude admission requires positive request/output allowances and finite positive price ceilings")
	}
	return nil
}

type AdmissionEvidence struct {
	SchemaVersion int             `json:"schema_version"`
	Limits        ClaudeAdmission `json:"limits"`
	Admitted      int             `json:"admitted_requests"`
	Refused       int             `json:"refused_requests"`
	Reasons       map[string]int  `json:"refusal_reasons"`
	Basis         string          `json:"basis"`
}

type requestAdmission struct {
	mu       sync.Mutex
	closed   bool
	path     string
	evidence AdmissionEvidence
}

func newRequestAdmission(output string, limits *ClaudeAdmission) (*requestAdmission, error) {
	if limits == nil {
		return nil, nil
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	a := &requestAdmission{path: filepath.Join(output, "claude-admission.json"), evidence: AdmissionEvidence{SchemaVersion: 1, Limits: *limits, Reasons: map[string]int{}, Basis: "Declared request/output/routing-price ceilings; not provider receipts or certified invoice enforcement"}}
	return a, a.save()
}

func (a *requestAdmission) save() error { return writeRelayMetadata(a.path, a.evidence) }

func (a *requestAdmission) refuse(reason string) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("admission closed")
	}
	a.evidence.Refused++
	a.evidence.Reasons[reason]++
	return a.save()
}

// Called after request validation and before forwarding. Reservations never refund,
// including failed upstream calls and errors writing later receipt metadata.
func (a *requestAdmission) admit() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("admission closed")
	}
	if a.evidence.Admitted >= a.evidence.Limits.MaxRequests {
		a.evidence.Refused++
		a.evidence.Reasons["request_allowance_exhausted"]++
		return errors.Join(errors.New("request allowance exhausted"), a.save())
	}
	a.evidence.Admitted++
	return a.save()
}

func (a *requestAdmission) prepare(raw map[string]json.RawMessage) error {
	if a == nil {
		return nil
	}
	if contextValue, ok := raw["context_management"]; ok {
		var contextRequest any
		if json.Unmarshal(contextValue, &contextRequest) != nil {
			return errors.New("unsupported context management")
		}
		// The installed CLI's keep-all marker changes no context. Normalize only
		// that exact no-op; automatic compaction or other edits are not admitted.
		keepAll := map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}}}
		if !reflect.DeepEqual(contextRequest, keepAll) {
			return errors.New("unsupported context management")
		}
		delete(raw, "context_management")
	}
	// Only the ordinary Anthropic-compatible message surface is admitted. Server
	// tools, plugins, containers, paid routing tiers and unknown extensions fail closed.
	allowed := map[string]bool{"model": true, "messages": true, "system": true, "max_tokens": true, "metadata": true, "stop_sequences": true, "stream": true, "temperature": true, "thinking": true, "tool_choice": true, "tools": true, "top_k": true, "top_p": true, "output_config": true, "provider": true}
	for field := range raw {
		if !allowed[field] {
			return errors.New("unsupported request extension")
		}
	}
	var structured any
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return err
	}
	if containsCacheControl(structured) {
		return errors.New("cache_control is unsupported by admission price contract")
	}
	var output int
	if err := json.Unmarshal(raw["max_tokens"], &output); err != nil || output < 1 || output > a.evidence.Limits.MaxOutputTokens {
		return errors.New("response token allowance exceeded or missing")
	}
	var tools []map[string]json.RawMessage
	if value, ok := raw["tools"]; ok {
		if err := json.Unmarshal(value, &tools); err != nil {
			return errors.New("invalid tool declarations")
		}
		for _, tool := range tools {
			if value, ok := tool["type"]; ok {
				var kind string
				if json.Unmarshal(value, &kind) != nil || kind != "custom" {
					return errors.New("unsupported billable server tool")
				}
			}
		}
	}
	provider, err := json.Marshal(map[string]any{"only": []string{"Z.AI"}, "allow_fallbacks": false, "max_price": a.evidence.Limits.MaxPrice})
	if err != nil {
		return err
	}
	raw["provider"] = provider
	return nil
}

func containsCacheControl(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "cache_control" || containsCacheControl(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if containsCacheControl(child) {
				return true
			}
		}
	}
	return false
}

func (a *requestAdmission) finish() error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	return a.save()
}
