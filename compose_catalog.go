package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// AMSL catalog wire shapes (schema_version + capabilities). These are parse-only
// helpers for loadCatalog; CatalogRecord/CatalogIndex remain the normalized API.

type amslCatalogFile struct {
	SchemaVersion int              `json:"schema_version"`
	Capabilities  []amslCapability `json:"capabilities"`
}

type amslCapability struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	Domain             string               `json:"domain"`
	Summary            string               `json:"summary"`
	Status             string               `json:"status"`
	SchemaVersion      int                  `json:"schema_version"`
	UseWhen            []string             `json:"use_when"`
	NonGoals           []string             `json:"non_goals"`
	ProposedGuarantees []string             `json:"proposed_guarantees"`
	FailureModes       []string             `json:"failure_modes"`
	Evidence           []json.RawMessage    `json:"evidence"`
	Alternatives       []json.RawMessage    `json:"alternatives"`
	Implementations    []amslImplementation `json:"implementations"`
	Verification       []json.RawMessage    `json:"verification"`
	OpenQuestions      []string             `json:"open_questions"`
	Disposition        string               `json:"disposition"`
}

type amslImplementation struct {
	Revision string `json:"revision"`
	URL      string `json:"url"`
}

type normalizedCatalogFile struct {
	SchemaVersion int             `json:"schema_version"`
	Root          string          `json:"root"`
	Pin           string          `json:"pin"`
	SourceLabel   string          `json:"source_label"`
	Records       []CatalogRecord `json:"records"`
}

// loadCatalog reads an offline catalog JSON file. It accepts the real AMSL shape
// {schema_version, capabilities} and the normalized {schema_version, records}
// shape. Pin is the SHA-256 of the exact source bytes (relocation-independent).
// Root is the absolute directory containing the file. Multiple implementation
// revisions of one capability become multiple records; rows without
// implementations are retained with an empty revision for inspection only.
func loadCatalog(path string) (CatalogIndex, error) {
	raw, err := composeReadBoundedRegularFile(path, composeCatalogJSONMax)
	if err != nil {
		return CatalogIndex{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return CatalogIndex{}, err
	}
	root := filepath.Dir(abs)
	pin := digest(raw)

	kind, err := detectCatalogShape(raw)
	if err != nil {
		return CatalogIndex{}, fmt.Errorf("catalog %s: %w", path, err)
	}

	var idx CatalogIndex
	switch kind {
	case "amsl":
		var file amslCatalogFile
		if err := decodeCatalogJSON(raw, &file); err != nil {
			return CatalogIndex{}, fmt.Errorf("catalog %s: %w", path, err)
		}
		if file.SchemaVersion != compositionSchemaVersion {
			return CatalogIndex{}, fmt.Errorf("catalog %s: unsupported schema_version %d", path, file.SchemaVersion)
		}
		records, err := normalizeAMSLCapabilities(file.Capabilities)
		if err != nil {
			return CatalogIndex{}, fmt.Errorf("catalog %s: %w", path, err)
		}
		idx = CatalogIndex{
			SchemaVersion: compositionSchemaVersion,
			Root:          root,
			Pin:           pin,
			SourceLabel:   "amsl-capabilities",
			Records:       records,
		}
	case "normalized":
		var file normalizedCatalogFile
		if err := decodeCatalogJSON(raw, &file); err != nil {
			return CatalogIndex{}, fmt.Errorf("catalog %s: %w", path, err)
		}
		if file.SchemaVersion != compositionSchemaVersion {
			return CatalogIndex{}, fmt.Errorf("catalog %s: unsupported schema_version %d", path, file.SchemaVersion)
		}
		if err := validateNormalizedRecords(file.Records); err != nil {
			return CatalogIndex{}, fmt.Errorf("catalog %s: %w", path, err)
		}
		label := file.SourceLabel
		if label == "" {
			label = "normalized-records"
		}
		idx = CatalogIndex{
			SchemaVersion: compositionSchemaVersion,
			Root:          root,
			Pin:           pin,
			SourceLabel:   label,
			Records:       append([]CatalogRecord(nil), file.Records...),
		}
	default:
		return CatalogIndex{}, fmt.Errorf("catalog %s: unrecognized catalog shape", path)
	}
	return idx, nil
}

// pinCatalog returns the content hash of the canonical catalog body with Root
// and Pin cleared so relocation does not change identity.
func pinCatalog(idx CatalogIndex) (string, error) {
	body := CatalogIndex{
		SchemaVersion: idx.SchemaVersion,
		SourceLabel:   idx.SourceLabel,
		Records:       idx.Records,
	}
	return hashCompositionValue(body)
}

// lookupCatalog returns the exact id+revision row (case-sensitive). Missing rows
// return an error. Empty-revision inspection rows are findable but are not
// executable reuse pins.
func lookupCatalog(idx CatalogIndex, id, revision string) (CatalogRecord, error) {
	for _, rec := range idx.Records {
		if rec.ID == id && rec.Revision == revision {
			return rec, nil
		}
	}
	return CatalogRecord{}, fmt.Errorf("catalog lookup miss: id %q revision %q", id, revision)
}

// listCatalog returns records for family, or all records when family is empty.
func listCatalog(idx CatalogIndex, family string) []CatalogRecord {
	if family == "" {
		out := make([]CatalogRecord, len(idx.Records))
		copy(out, idx.Records)
		return out
	}
	var out []CatalogRecord
	for _, rec := range idx.Records {
		if rec.Family == family {
			out = append(out, rec)
		}
	}
	return out
}

func detectCatalogShape(raw []byte) (string, error) {
	if err := composeRejectDuplicateJSONKeys(raw); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return "", err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("must contain exactly one JSON object")
	}
	_, hasCaps := top["capabilities"]
	_, hasRecords := top["records"]
	switch {
	case hasCaps && hasRecords:
		return "", fmt.Errorf("catalog must not declare both capabilities and records")
	case hasCaps:
		return "amsl", nil
	case hasRecords:
		return "normalized", nil
	default:
		return "", fmt.Errorf("catalog requires capabilities or records")
	}
}

func decodeCatalogJSON(raw []byte, dest any) error {
	return composeDecodeStrictJSON(raw, dest, "catalog")
}

func normalizeAMSLCapabilities(caps []amslCapability) ([]CatalogRecord, error) {
	seen := map[string]struct{}{}
	var out []CatalogRecord
	for _, cap := range caps {
		if strings.TrimSpace(cap.ID) == "" {
			return nil, fmt.Errorf("capability missing id")
		}
		if cap.SchemaVersion == 0 {
			return nil, fmt.Errorf("capability %q missing schema_version", cap.ID)
		}
		if cap.SchemaVersion != compositionSchemaVersion {
			return nil, fmt.Errorf("capability %q unsupported schema_version %d", cap.ID, cap.SchemaVersion)
		}
		base := CatalogRecord{
			ID:         cap.ID,
			Family:     cap.Domain,
			Summary:    cap.Summary,
			Maturity:   cap.Status, // recorded label only; never promoted
			Interfaces: append([]string(nil), cap.UseWhen...),
			Guarantees: append([]string(nil), cap.ProposedGuarantees...),
			NonGoals:   append([]string(nil), cap.NonGoals...),
			Extra:      amslExtra(cap),
		}
		if len(cap.Implementations) == 0 {
			key := catalogIdentityKey(cap.ID, "")
			if _, ok := seen[key]; ok {
				return nil, fmt.Errorf("duplicate catalog identity %s", key)
			}
			seen[key] = struct{}{}
			rec := base
			rec.Revision = ""
			out = append(out, rec)
			continue
		}
		for _, impl := range cap.Implementations {
			rev := impl.Revision
			key := catalogIdentityKey(cap.ID, rev)
			if _, ok := seen[key]; ok {
				return nil, fmt.Errorf("duplicate catalog identity %s", key)
			}
			seen[key] = struct{}{}
			rec := base
			rec.Revision = rev
			if impl.URL != "" {
				if rec.Extra == nil {
					rec.Extra = map[string]string{}
				}
				rec.Extra = copyStringMap(rec.Extra)
				rec.Extra["implementation_url"] = impl.URL
			}
			out = append(out, rec)
		}
	}
	return out, nil
}

func amslExtra(cap amslCapability) map[string]string {
	extra := map[string]string{}
	if cap.Name != "" {
		extra["name"] = cap.Name
	}
	if cap.Disposition != "" {
		extra["disposition"] = cap.Disposition
	}
	if cap.SchemaVersion != 0 {
		extra["capability_schema_version"] = fmt.Sprintf("%d", cap.SchemaVersion)
	}
	if len(cap.FailureModes) > 0 {
		extra["failure_modes"] = strings.Join(cap.FailureModes, "\n")
	}
	if len(cap.OpenQuestions) > 0 {
		extra["open_questions"] = strings.Join(cap.OpenQuestions, "\n")
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func validateNormalizedRecords(recs []CatalogRecord) error {
	seen := map[string]struct{}{}
	for _, rec := range recs {
		if strings.TrimSpace(rec.ID) == "" {
			return fmt.Errorf("record missing id")
		}
		key := catalogIdentityKey(rec.ID, rec.Revision)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate catalog identity %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func catalogIdentityKey(id, revision string) string {
	return id + "@" + revision
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// immutableCatalogRevision reports whether revision is an immutable 40- or 64-digit hex pin.
func immutableCatalogRevision(revision string) bool {
	n := len(revision)
	if n != 40 && n != 64 {
		return false
	}
	for i := 0; i < n; i++ {
		c := revision[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
