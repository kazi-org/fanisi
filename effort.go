package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// Effort is an offline attribution, not a price estimate. Token subsets are
// retained for auditing; they never add to input plus output.
type Effort struct {
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"id"`
	Study         string             `json:"study"`
	Task          string             `json:"task"`
	Attempt       string             `json:"attempt"`
	Role          string             `json:"role"`
	Source        string             `json:"source_fingerprint"`
	From          time.Time          `json:"from"`
	To            time.Time          `json:"to"`
	Tokens        *coordinatorTokens `json:"tokens"`
	Cost          *float64           `json:"cost_usd"`
	Seconds       *float64           `json:"active_seconds"`
	Coverage      string             `json:"coverage"`
	Allocation    string             `json:"allocation"`
}

func validateEffort(e Effort) error {
	if e.SchemaVersion != 1 || !simpleID(e.ID) || !simpleID(e.Study) || e.Source == "" || e.From.IsZero() || !e.To.After(e.From) {
		return errors.New("effort requires version 1, identity, source and increasing time window")
	}
	switch e.Role {
	case "coordinator", "reviewer", "preparation", "tooling":
	default:
		return errors.New("invalid effort role")
	}
	if e.Coverage != "complete" && e.Coverage != "partial" {
		return errors.New("effort coverage must be complete or partial")
	}
	if e.Allocation != "exclusive" && e.Allocation != "study" {
		return errors.New("allocation must be exclusive or study")
	}
	if e.Allocation == "exclusive" && (!simpleID(e.Task) || e.Attempt == "") {
		return errors.New("exclusive effort requires task and attempt")
	}
	if e.Allocation == "study" && (e.Task != "" || e.Attempt != "" || (e.Role != "preparation" && e.Role != "tooling")) {
		return errors.New("shared preparation/tooling must remain at study scope")
	}
	if e.Tokens != nil && (!validCoordinatorTokens(*e.Tokens) || e.Tokens.Total != e.Tokens.Input+e.Tokens.Output) {
		return errors.New("inconsistent effort token subsets or total")
	}
	for _, n := range []*float64{e.Cost, e.Seconds} {
		if n != nil && (*n < 0 || math.IsNaN(*n) || math.IsInf(*n, 0)) {
			return errors.New("invalid effort cost or duration")
		}
	}
	if e.Seconds != nil && *e.Seconds > e.To.Sub(e.From).Seconds() {
		return errors.New("active effort exceeds window")
	}
	return nil
}

func loadEfforts(root string) ([]Effort, error) {
	paths, err := filepath.Glob(filepath.Join(root, "effort-*.json"))
	if err != nil {
		return nil, err
	}
	records := []Effort{}
	for _, p := range paths {
		var e Effort
		if err := readStrictJSON(p, &e); err != nil {
			return nil, err
		}
		if err := validateEffort(e); err != nil {
			return nil, fmt.Errorf("effort %s: %w", e.ID, err)
		}
		records = append(records, e)
	}
	return records, nil
}

func importEffort(root, file string) error { return importEffortSource(root, file, "") }

func importEffortSource(root, file, coordinator string) error {
	var e Effort
	if err := readStrictJSON(file, &e); err != nil {
		return err
	}
	if coordinator != "" {
		if err := coordinatorEffort(coordinator, &e); err != nil {
			return err
		}
	}
	if err := validateEffort(e); err != nil {
		return err
	}
	if e.Allocation == "exclusive" {
		if filepath.IsAbs(e.Attempt) || filepath.Clean(e.Attempt) == ".." || !filepath.IsLocal(e.Attempt) {
			return errors.New("attempt must be relative to study directory")
		}
		var a Attempt
		if err := readJSON(filepath.Join(root, e.Attempt, "attempt.json"), &a); err != nil {
			return err
		}
		if a.TaskID != e.Task {
			return errors.New("effort task does not match attempt")
		}
	}
	lock := filepath.Join(root, ".effort-import-lock")
	if err := os.Mkdir(lock, 0700); err != nil {
		return fmt.Errorf("acquiring effort import lock: %w", err)
	}
	defer os.Remove(lock)
	records, err := loadEfforts(root)
	if err != nil {
		return err
	}
	for _, old := range records {
		if old.ID == e.ID {
			if reflect.DeepEqual(old, e) {
				return nil
			}
			return errors.New("conflicting effort identity")
		}
		if old.Source == e.Source && e.From.Before(old.To) && old.From.Before(e.To) {
			return errors.New("overlapping source attribution")
		}
	}
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(filepath.Join(root, "effort-"+e.ID+".json"), append(b, '\n'))
}

// Coordinator receipts are consumed directly; transcripts are not reparsed here.
func coordinatorEffort(file string, e *Effort) error {
	var c struct {
		SchemaVersion int               `json:"schema_version"`
		Snapshot      string            `json:"snapshot_sha256"`
		From          time.Time         `json:"from"`
		To            time.Time         `json:"to"`
		Tokens        coordinatorTokens `json:"tokens"`
		Cost          *float64          `json:"cost_usd"`
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	if c.SchemaVersion != 1 || c.Snapshot == "" {
		return errors.New("unsupported coordinator receipt")
	}
	e.Source = c.Snapshot
	e.From = c.From
	e.To = c.To
	e.Tokens = &c.Tokens
	e.Cost = c.Cost
	e.Role = "coordinator"
	e.Coverage = "partial"
	return validateEffort(*e)
}

type EffortTotal struct {
	Records         int      `json:"records"`
	Input           int64    `json:"input_tokens"`
	Cached          int64    `json:"cached_input_tokens"`
	Output          int64    `json:"output_tokens"`
	Reasoning       int64    `json:"reasoning_output_tokens"`
	KnownCost       float64  `json:"known_cost_usd"`
	Cost            *float64 `json:"total_cost_usd"`
	UnknownPrices   int      `json:"records_without_price"`
	ActiveSeconds   float64  `json:"known_active_seconds"`
	UnknownDuration int      `json:"records_without_duration"`
}

func effortReport(root string) (map[string]*EffortTotal, error) {
	records, err := loadEfforts(root)
	if err != nil {
		return nil, err
	}
	totals := map[string]*EffortTotal{}
	for _, e := range records {
		key := e.Role
		if e.Allocation == "study" {
			key = "study_" + key
		}
		r := totals[key]
		if r == nil {
			r = &EffortTotal{}
			totals[key] = r
		}
		r.Records++
		if e.Tokens != nil {
			r.Input += e.Tokens.Input
			r.Cached += e.Tokens.Cached
			r.Output += e.Tokens.Output
			r.Reasoning += e.Tokens.Reasoning
		}
		if e.Cost == nil {
			r.UnknownPrices++
		} else {
			r.KnownCost += *e.Cost
		}
		if e.Seconds == nil {
			r.UnknownDuration++
		} else {
			r.ActiveSeconds += *e.Seconds
		}
	}
	for _, r := range totals {
		if r.UnknownPrices == 0 {
			cost := r.KnownCost
			r.Cost = &cost
		}
	}
	return totals, nil
}
