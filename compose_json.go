package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	// composeExecutableJSONMax bounds request/decision/manifest/impl/evidence/
	// draft/finite-choice JSON loaded at the compose CLI and backend seams.
	composeExecutableJSONMax = 256 << 10
	// composeCatalogJSONMax allows larger offline AMSL indexes than executable records.
	composeCatalogJSONMax = 8 << 20
)

// composeDecodeStrictJSON decodes exactly one JSON value into dest with unknown
// fields rejected and duplicate object keys rejected via a token walk. data is
// not mutated.
func composeDecodeStrictJSON(data []byte, dest any, source string) error {
	if source == "" {
		source = "JSON"
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("%s is empty", source)
	}
	if err := composeRejectDuplicateJSONKeys(data); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("decode %s: %w", source, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%s must contain exactly one JSON value", source)
	}
	return nil
}

// composeReadStrictJSONFile reads a regular file at most max bytes (FIFOs and
// other special files rejected before open) and decodes with composeDecodeStrictJSON.
func composeReadStrictJSONFile(path string, dest any, max int) error {
	data, err := composeReadBoundedRegularFile(path, max)
	if err != nil {
		return err
	}
	return composeDecodeStrictJSON(data, dest, path)
}

// composeRejectDuplicateJSONKeys walks JSON tokens and rejects duplicate keys
// within the same object (including nested objects). Arrays and scalars pass.
func composeRejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := composeWalkJSONRejectDupKeys(dec); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("must contain exactly one JSON value")
	}
	return nil
}

func composeWalkJSONRejectDupKeys(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			seen := map[string]struct{}{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyTok.(string)
				if !ok {
					return fmt.Errorf("expected object key string")
				}
				if _, dup := seen[key]; dup {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := composeWalkJSONRejectDupKeys(dec); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil {
				return err
			}
			if end != json.Delim('}') {
				return fmt.Errorf("expected end of object")
			}
			return nil
		case '[':
			for dec.More() {
				if err := composeWalkJSONRejectDupKeys(dec); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil {
				return err
			}
			if end != json.Delim(']') {
				return fmt.Errorf("expected end of array")
			}
			return nil
		default:
			return fmt.Errorf("unexpected delimiter %q", t)
		}
	case nil, bool, float64, json.Number, string:
		return nil
	default:
		return fmt.Errorf("unsupported JSON token %T", tok)
	}
}
