package main

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestGenerationStreamDoneTrailer(t *testing.T) {
	t.Parallel()
	start := "data: {\"type\":\"message_start\",\"message\":{\"id\":\"gen-trailer\"}}\n\n"
	stop := "data: {\"type\":\"message_stop\"}\n\n"
	for _, tc := range []struct {
		name, input string
		gaps        int
		ids         int
	}{
		{"completed_crlf", start + stop + "event: data\r\ndata: [DONE]\r\n\r\n", 0, 1},
		{"completed_eof", start + stop + "data: [DONE]", 0, 1},
		{"without_identity", stop + "data: [DONE]\n", 1, 0},
		{"before_stop", start + "data: [DONE]\n" + stop, 1, 1},
		{"malformed_trailer", start + stop + "data: [DONE]junk\n", 1, 1},
		{"prior_gap_retained", start + "data: invalid\n" + stop + "data: [DONE]\n", 1, 1},
		{"new_unfinished_message", start + stop + start + "data: [DONE]\n", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gaps, ids := 0, 0
			reader := &generationStream{ReadCloser: io.NopCloser(iotest.OneByteReader(strings.NewReader(tc.input))), onID: func(string) error { ids++; return nil }, onUnknown: func() error { gaps++; return nil }}
			got, err := io.ReadAll(reader)
			if err != nil || string(got) != tc.input || gaps != tc.gaps || ids != tc.ids {
				t.Fatalf("err=%v preserved=%v gaps=%d want=%d ids=%d want=%d", err, string(got) == tc.input, gaps, tc.gaps, ids, tc.ids)
			}
		})
	}
}
