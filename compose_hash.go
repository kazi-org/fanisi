package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// canonicalCompositionJSON returns UTF-8 JSON with sorted object keys and no
// insignificant whitespace. Top-level content_sha256 is omitted. Numbers are
// preserved losslessly via json.Number. The input value is not mutated.
func canonicalCompositionJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var node any
	if err := dec.Decode(&node); err != nil {
		return nil, err
	}
	if m, ok := node.(map[string]any); ok {
		delete(m, "content_sha256")
	}
	var buf bytes.Buffer
	if err := writeCanonicalCompositionJSON(&buf, node); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalCompositionJSON(buf *bytes.Buffer, node any) error {
	switch v := node.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if v {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(string(v))
	case string:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, elem := range v {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalCompositionJSON(buf, elem); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonicalCompositionJSON(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", v)
	}
	return nil
}

func hashCompositionValue(v any) (string, error) {
	b, err := canonicalCompositionJSON(v)
	if err != nil {
		return "", err
	}
	return digest(b), nil
}

func hashCompositionRequest(r CompositionRequest) (string, error) {
	return hashCompositionValue(r)
}

func hashComponentDecision(d ComponentDecision) (string, error) {
	return hashCompositionValue(d)
}

func hashApplicationManifest(m ApplicationManifest) (string, error) {
	return hashCompositionValue(m)
}

func hashImplementationRequest(r ImplementationRequest) (string, error) {
	return hashCompositionValue(r)
}

func hashAttemptResult(r AttemptResult) (string, error) {
	return hashCompositionValue(r)
}
