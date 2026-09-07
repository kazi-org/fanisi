package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestGenerationStreamPreservesFragmentedBytesAndOnlyCapturesIDs(t *testing.T) {
	frame := `data: {"type":"message_start","message":{"id":"gen-early","content":"private source"}}` + "\r\n\r\n"
	stop := `data: {"type":"message_stop"}` + "\n\n"
	input := "event: message_start\n" + frame + frame + stop + "data: " + strings.Repeat("x", 70<<10) + "\n" +
		`data: {"type":"message_start","message":{"id":"not a generation"}}` + "\n" +
		`data: {"type":"message_start","message":{"id":"gen-after-overflow"}}` + "\n" + `data: {"type":"message_stop"}`
	var ids []string
	stops := 0
	unknown := 0
	reader := &generationStream{ReadCloser: io.NopCloser(iotest.OneByteReader(strings.NewReader(input))), onID: func(id string) error { ids = append(ids, id); return nil }, onStop: func() error { stops++; return nil }, onUnknown: func() error { unknown++; return nil }}
	got, err := io.ReadAll(reader)
	if err != nil || string(got) != input {
		t.Fatalf("stream changed or EOF corrupted: %v", err)
	}
	if unknown != 1 {
		t.Fatalf("unknown identity was hidden: %d", unknown)
	}
	if stops != 2 {
		t.Fatalf("lost completion events: %d", stops)
	}
	if len(ids) != 2 || ids[0] != "gen-early" || ids[1] != "gen-after-overflow" {
		t.Fatalf("incorrect IDs: %v", ids)
	}
}

func TestGenerationStreamSurfacesRecordingFailure(t *testing.T) {
	fail := errors.New("cannot retain identity")
	reader := &generationStream{ReadCloser: io.NopCloser(strings.NewReader(`data: {"type":"message_start","message":{"id":"gen-one"}}` + "\n")), onID: func(string) error { return fail }}
	_, err := io.ReadAll(reader)
	if !errors.Is(err, fail) {
		t.Fatalf("recording failure hidden: %v", err)
	}
}
