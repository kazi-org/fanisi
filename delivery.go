package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type DeliveryReport struct {
	Attempts           int                `json:"attempts"`
	Accepted           int                `json:"accepted_landed_tasks"`
	Autonomous         int                `json:"autonomous_accepted_tasks"`
	Assisted           int                `json:"assisted_accepted_tasks"`
	VerifiedPending    int                `json:"verified_pending_attempts"`
	KnownCost          float64            `json:"known_cost_lower_bound_usd"`
	TotalCost          *float64           `json:"total_cost_usd"`
	CostPerAutonomous  *float64           `json:"cost_per_autonomous_accepted_usd"`
	MissingReceipts    int                `json:"attempts_without_complete_receipts"`
	ReviewUnknown      int                `json:"reviews_without_measurement"`
	KnownReviewSeconds float64            `json:"known_review_seconds"`
	Tokens             int                `json:"worker_input_plus_output_tokens"`
	Elapsed            map[string]float64 `json:"request_to_landing_seconds"`
	CIWait             *float64           `json:"ci_wait_seconds"`
	Notes              []string           `json:"notes"`
}

func deliveryReport(root string, paths []string) (DeliveryReport, error) {
	d := DeliveryReport{Elapsed: map[string]float64{}, Notes: []string{"Known cost is a lower bound; missing coordinator/reviewer prices prevent a total-dollar verdict.", "Study preparation and tooling are reported separately without amortization. CI wait is unmeasured. Historical reviewed acceptance is distinct from verified landing."}}
	seen := map[string]Receipt{}
	accepted := map[string]bool{}
	start := map[string]time.Time{}
	landedAt := map[string]time.Time{}
	for _, p := range paths {
		dir := filepath.Dir(p)
		var a Attempt
		if err := readJSON(p, &a); err != nil {
			return d, err
		}
		d.Attempts++
		if !a.StartedAt.IsZero() && (start[a.TaskID].IsZero() || a.StartedAt.Before(start[a.TaskID])) {
			start[a.TaskID] = a.StartedAt
		}
		var ledger ReceiptLedger
		if err := readJSON(filepath.Join(dir, "provider-ledger.json"), &ledger); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return d, err
			}
			d.MissingReceipts++
		} else {
			if !ledger.Complete || len(ledger.Generations) == 0 {
				d.MissingReceipts++
			}
			// Legacy aggregates remain a lower bound, never evidence of full coverage.
			if len(ledger.Generations) == 0 {
				d.KnownCost += ledger.KnownCost
				d.Tokens += ledger.KnownTokens
			}
			for _, r := range ledger.Generations {
				if r.ID == "" || r.Provider == "" {
					return d, errors.New("receipt requires provider and generation identity")
				}
				key := r.Provider + "\x00" + r.ID
				if old, ok := seen[key]; ok {
					if !reflect.DeepEqual(old, r) {
						return d, errors.New("conflicting provider receipt")
					}
					continue
				}
				seen[key] = r
				if r.Cost != nil {
					if *r.Cost < 0 {
						return d, errors.New("negative receipt cost")
					}
					d.KnownCost += *r.Cost
				}
				if r.Prompt != nil && r.Output != nil {
					if *r.Prompt < 0 || *r.Output < 0 || (r.Cached != nil && (*r.Cached < 0 || *r.Cached > *r.Prompt)) || (r.Reasoning != nil && (*r.Reasoning < 0 || *r.Reasoning > *r.Output)) {
						return d, errors.New("inconsistent receipt subsets")
					}
					d.Tokens += *r.Prompt + *r.Output
				}
			}
		}
		reviews, err := filepath.Glob(filepath.Join(dir, "review-*.json"))
		if err != nil {
			return d, err
		}
		sort.Strings(reviews)
		latest := ""
		for _, p := range reviews {
			var r Review
			if err := readJSON(p, &r); err != nil {
				return d, err
			}
			latest = filepath.Base(p)
			if r.Seconds == nil {
				d.ReviewUnknown++
			} else {
				d.KnownReviewSeconds += *r.Seconds
			}
		}
		landings, err := filepath.Glob(filepath.Join(dir, "landing-*.json"))
		if err != nil {
			return d, err
		}
		records := []Landing{}
		revoked := map[string]bool{}
		for _, p := range landings {
			var l Landing
			if err := readStrictJSON(p, &l); err != nil {
				return d, err
			}
			records = append(records, l)
			if l.Revokes != "" {
				revoked[l.Revokes] = true
			}
		}
		landed := false
		for _, l := range records {
			if l.Revokes != "" || revoked[l.ID] || l.Review != latest {
				continue
			}
			if err := verifyLanding(context.Background(), dir, l); err != nil {
				return d, fmt.Errorf("landing %s: %w", l.ID, err)
			}
			landed = true
			accepted[a.TaskID] = accepted[a.TaskID] || a.RepairFrom != ""
			if !l.At.IsZero() && (landedAt[a.TaskID].IsZero() || l.At.Before(landedAt[a.TaskID])) {
				landedAt[a.TaskID] = l.At
			}
		}
		if a.ScopeOK && a.VerificationPassed && !landed {
			d.VerifiedPending++
		}
	}
	for task, assisted := range accepted {
		d.Accepted++
		if assisted {
			d.Assisted++
		} else {
			d.Autonomous++
		}
		if !start[task].IsZero() && !landedAt[task].Before(start[task]) {
			d.Elapsed[task] = landedAt[task].Sub(start[task]).Seconds()
		}
	}
	effort, err := effortReport(root)
	if err != nil {
		return d, err
	}
	for role, r := range effort {
		if strings.HasPrefix(role, "study_") {
			continue
		}
		d.KnownCost += r.KnownCost
	}
	// An import's absence cannot prove that coordination and review were free.
	// This report deliberately declines a total until a study coverage contract exists.
	return d, nil
}
