package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

func dispatchDirectories(dir string) ([]string, bool, error) {
	var m DispatchManifest
	if err := readStrictJSON(filepath.Join(dir, "dispatch-manifest.json"), &m); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, true, err
	}
	if m.SchemaVersion != 1 || m.MaxDispatches < 1 || m.MaxDispatches > 2 || len(m.Dispatches) > m.MaxDispatches {
		return nil, true, errors.New("invalid evaluation dispatch manifest")
	}
	paths := []string{}
	for i, d := range m.Dispatches {
		if d.Directory != fmt.Sprintf("dispatch-%04d", i+1) {
			return nil, true, errors.New("dispatch manifest contains unexpected directory identity")
		}
		path := filepath.Join(dir, d.Directory)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, true, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, true, errors.New("dispatch directory is not local regular directory")
		}
		paths = append(paths, path)
	}
	return paths, true, nil
}

func reconcileDispatches(ctx context.Context, dir, key string, client *http.Client, paths []string) error {
	var failures []error
	for _, path := range paths {
		if err := reconcileWithClient(ctx, path, key, client); err != nil {
			failures = append(failures, err)
		}
	}
	if err := aggregateDispatchLedgers(dir, paths); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func aggregateDispatchLedgers(dir string, paths []string) error {
	ledger := ReceiptLedger{SchemaVersion: 1, Generations: []Receipt{}, Unresolved: []string{}, RetrievedAt: time.Now().UTC()}
	seen := map[string]Receipt{}
	if len(paths) == 0 {
		ledger.Unresolved = append(ledger.Unresolved, "no admitted dispatch receipts")
	}
	for _, path := range paths {
		var child ReceiptLedger
		if err := readJSON(filepath.Join(path, "provider-ledger.json"), &child); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			ledger.Unresolved = append(ledger.Unresolved, filepath.Base(path)+": missing receipt ledger")
			continue
		}
		if child.SchemaVersion != 1 {
			return errors.New("unsupported dispatch receipt schema")
		}
		if !child.Complete || len(child.Generations) == 0 {
			ledger.Unresolved = append(ledger.Unresolved, filepath.Base(path)+": incomplete receipt coverage")
		}
		for _, gap := range child.Unresolved {
			ledger.Unresolved = append(ledger.Unresolved, filepath.Base(path)+": "+gap)
		}
		for _, r := range child.Generations {
			if r.ID == "" || r.Provider == "" {
				return errors.New("dispatch receipt missing provider/request identity")
			}
			key := r.Provider + "\x00" + r.ID
			if prior, ok := seen[key]; ok {
				if !reflect.DeepEqual(prior, r) {
					return errors.New("conflicting dispatch receipt identity")
				}
				continue
			}
			seen[key] = r
			ledger.Generations = append(ledger.Generations, r)
			if r.Cost != nil {
				if *r.Cost < 0 {
					return errors.New("negative dispatch receipt cost")
				}
				ledger.KnownCost += *r.Cost
			} else {
				ledger.Unresolved = append(ledger.Unresolved, r.ID+": missing cost")
			}
			if r.Prompt != nil && r.Output != nil {
				if *r.Prompt < 0 || *r.Output < 0 || (r.Cached != nil && (*r.Cached < 0 || *r.Cached > *r.Prompt)) || (r.Reasoning != nil && (*r.Reasoning < 0 || *r.Reasoning > *r.Output)) {
					return errors.New("invalid dispatch token subsets")
				}
				ledger.KnownTokens += *r.Prompt + *r.Output
			} else {
				ledger.Unresolved = append(ledger.Unresolved, r.ID+": missing tokens")
			}
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
	if !ledger.Complete {
		return errors.New("dispatch receipt coverage incomplete; total remains unknown")
	}
	return nil
}
