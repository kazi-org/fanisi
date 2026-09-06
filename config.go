package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
)

const schemaVersion = 1

type ContextSlice struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	Lines int    `json:"lines"`
}

type Pricing struct {
	Input  float64 `json:"input_usd_per_million"`
	Output float64 `json:"output_usd_per_million"`
	Source string  `json:"source"`
}

type Config struct {
	SchemaVersion   int            `json:"schema_version"`
	Workspace       string         `json:"workspace"`
	Output          string         `json:"output"`
	KeyFile         string         `json:"key_file,omitempty"`
	PromptFile      string         `json:"prompt_file"`
	ReadPaths       []string       `json:"read_paths,omitempty"`
	WritePaths      []string       `json:"write_paths"`
	Context         []ContextSlice `json:"context,omitempty"`
	FormatCommand   []string       `json:"format_command,omitempty"`
	VerifyCommand   []string       `json:"verify_command"`
	MaxCalls        int            `json:"max_calls"`
	MaxSeconds      int            `json:"max_seconds"`
	MaxCost         float64        `json:"max_cost_usd"`
	MaxTokens       int            `json:"max_tokens_total"`
	MaxOutputTokens int            `json:"max_output_tokens"`
	MaxContextBytes int            `json:"max_context_bytes"`
	PacketBytes     int            `json:"packet_bytes"`
	Reasoning       string         `json:"reasoning"`
	Pricing         Pricing        `json:"pricing"`
}

func loadConfig(path string) (Config, error) {
	cfg := Config{MaxCalls: 12, MaxSeconds: 1200, MaxCost: 0.2, MaxTokens: 400000, MaxOutputTokens: 8192, MaxContextBytes: 140000, PacketBytes: 16000, Reasoning: "medium"}
	if err := readStrictJSON(path, &cfg); err != nil {
		return cfg, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return cfg, err
	}
	for _, p := range []*string{&cfg.Workspace, &cfg.Output, &cfg.KeyFile, &cfg.PromptFile} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	return cfg, validateConfig(cfg)
}

func validateConfig(cfg Config) error {
	if cfg.SchemaVersion != schemaVersion {
		return fmt.Errorf("unsupported schema_version %d; want %d", cfg.SchemaVersion, schemaVersion)
	}
	if cfg.Workspace == "" || cfg.Output == "" || cfg.PromptFile == "" || len(cfg.WritePaths) == 0 || len(cfg.VerifyCommand) == 0 || cfg.VerifyCommand[0] == "" {
		return errors.New("workspace, output, prompt_file, write_paths and verify_command are required")
	}
	if len(cfg.FormatCommand) > 0 && cfg.FormatCommand[0] == "" {
		return errors.New("empty formatter executable")
	}
	if cfg.MaxCalls < 1 || cfg.MaxCalls > 100 || cfg.MaxSeconds < 1 || cfg.MaxSeconds > 7200 || cfg.MaxCost <= 0 || math.IsNaN(cfg.MaxCost) || math.IsInf(cfg.MaxCost, 0) || cfg.MaxTokens < 1 || cfg.MaxOutputTokens < 1 || cfg.MaxOutputTokens > 32768 || cfg.PacketBytes < 1 || cfg.PacketBytes > cfg.MaxContextBytes || cfg.MaxContextBytes > 1024*1024 {
		return errors.New("invalid call, time, spend, token or context budget")
	}
	if cfg.Pricing.Input <= 0 || cfg.Pricing.Output <= 0 || math.IsNaN(cfg.Pricing.Input) || math.IsNaN(cfg.Pricing.Output) || math.IsInf(cfg.Pricing.Input, 0) || math.IsInf(cfg.Pricing.Output, 0) || cfg.Pricing.Source == "" {
		return errors.New("explicit positive admission pricing and its source are required; actual cost uses provider receipts")
	}
	if cfg.Reasoning != "medium" && cfg.Reasoning != "low" {
		return errors.New("reasoning must be medium or low")
	}
	for _, p := range append(slices.Clone(cfg.ReadPaths), cfg.WritePaths...) {
		if err := validRelativePath(p); err != nil {
			return err
		}
	}
	for _, s := range cfg.Context {
		if s.Start < 1 || s.Lines < 1 || s.Lines > 120 {
			return errors.New("context slices need start >= 1 and 1..120 lines")
		}
		if !slices.Contains(cfg.readable(), s.Path) {
			return fmt.Errorf("context path %s is outside read scope", s.Path)
		}
	}
	return nil
}

func (cfg Config) readable() []string { return append(slices.Clone(cfg.ReadPaths), cfg.WritePaths...) }

func validRelativePath(p string) error {
	if !filepath.IsLocal(p) || filepath.Clean(p) != p || p == "." {
		return fmt.Errorf("scope path %q must be clean and relative to workspace", p)
	}
	for _, part := range splitPath(p) {
		if part == ".git" {
			return errors.New("git metadata is outside tool scope")
		}
	}
	return nil
}

func snapshot(cfg Config) (map[string]string, error) {
	result := map[string]string{}
	for _, name := range cfg.WritePaths {
		path, err := scopedPath(cfg.Workspace, name, cfg.WritePaths)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			result[name] = "missing"
			continue
		}
		if err != nil {
			return nil, err
		}
		result[name] = digest(b)
	}
	return result, nil
}

func changed(a, b map[string]string) bool {
	if len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return true
		}
	}
	return false
}
