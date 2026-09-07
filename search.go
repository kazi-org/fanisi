package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type SearchHit struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// searchFile locates symbols without spending model calls on sequential reads.
// The result is evidence only; searching cannot invalidate a verified snapshot.
func searchFile(ctx context.Context, cfg Config, path, query string, start int) (any, error) {
	if query == "" || len(query) > 200 || strings.ContainsAny(query, "\r\n") {
		return nil, errors.New("search requires a nonempty single-line literal query of at most 200 bytes")
	}
	if start == 0 {
		start = 1
	}
	if start < 1 {
		return nil, errors.New("search start must be positive")
	}
	full, err := scopedPath(cfg.Workspace, path, cfg.readable())
	if err != nil {
		return nil, err
	}
	file, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, errors.New("file exceeds the 2 MiB search limit; use bounded reads")
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return nil, errors.New("search requires UTF-8 text")
	}
	lines := strings.Split(string(data), "\n")
	hits := []SearchHit{}
	used := 0
	next := len(lines) + 1
	truncated := false
	for i := start - 1; i < len(lines); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		at := strings.Index(lines[i], query)
		if at < 0 {
			continue
		}
		lo, hi := max(0, at-120), min(len(lines[i]), at+len(query)+120)
		for lo > 0 && !utf8.RuneStart(lines[i][lo]) {
			lo--
		}
		for hi < len(lines[i]) && !utf8.RuneStart(lines[i][hi]) {
			hi++
		}
		excerpt := lines[i][lo:hi]
		if lo > 0 {
			excerpt = "…" + excerpt
		}
		if hi < len(lines[i]) {
			excerpt += "…"
		}
		hit := SearchHit{Line: i + 1, Text: excerpt}
		encoded, err := json.Marshal(hit)
		if err != nil {
			return nil, err
		}
		if len(hits) == 20 || used+len(encoded) > 6000 {
			next = i + 1
			truncated = true
			break
		}
		hits = append(hits, hit)
		used += len(encoded)
	}
	return map[string]any{"path": path, "query": query, "sha256": digest(data), "matches": hits, "next_start": next, "truncated": truncated}, nil
}
