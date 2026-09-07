package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// generationStream forwards bytes unchanged while retaining only early billing
// identities. An interrupted Claude reply may never reach its assistant log.
type generationStream struct {
	io.ReadCloser
	line      []byte
	overflow  bool
	seen      map[string]bool
	onID      func(string) error
	onStop    func() error
	onUnknown func() error
	stopped   bool
}

func (s *generationStream) Read(p []byte) (int, error) {
	n, readErr := s.ReadCloser.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			if err := s.capture(); err != nil {
				return n, errors.Join(readErr, err)
			}
			s.line, s.overflow = s.line[:0], false
		} else if len(s.line) < 64<<10 && !s.overflow {
			s.line = append(s.line, b)
		} else {
			s.line, s.overflow = nil, true
		}
	}
	if readErr != nil {
		if err := s.capture(); err != nil {
			readErr = errors.Join(readErr, err)
		}
	}
	return n, readErr
}

func (s *generationStream) capture() error {
	if s.overflow {
		return s.unknown()
	}
	if !bytes.HasPrefix(s.line, []byte("data:")) {
		return nil
	}
	var event struct {
		Type    string `json:"type"`
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if json.Unmarshal(bytes.TrimSpace(s.line[5:]), &event) != nil {
		return s.unknown()
	}
	if event.Type == "message_stop" && !s.stopped {
		s.stopped = true
		if s.onStop != nil {
			return s.onStop()
		}
	}
	if event.Type != "message_start" {
		return nil
	}
	id := event.Message.ID
	if !strings.HasPrefix(id, "gen-") || len(id) > 256 || !simpleID(id) {
		return s.unknown()
	}
	if s.seen[id] {
		return nil
	}
	if len(s.seen) >= 8 {
		return errors.Join(errors.New("too many generation identities in one response"), s.unknown())
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[id] = true
	s.stopped = false
	return s.onID(id)
}

// A skipped frame could contain another billing identity. Retain a coverage gap
// even if a later stop event arrives; forwarding bytes does not prove accounting.
func (s *generationStream) unknown() error {
	if s.onUnknown != nil {
		return s.onUnknown()
	}
	return nil
}
