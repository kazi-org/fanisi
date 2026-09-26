package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// providerRelay is process-local. It exposes one authenticated loopback endpoint,
// fixes model/provider routing, and never sends the real API key to the child.
func providerRelay(key, output string, limits ...*ClaudeAdmission) (endpoint, token string, closeRelay func() error, err error) {
	target, _ := url.Parse("https://openrouter.ai/api")
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return
	}
	token = hex.EncodeToString(nonce)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	admission, initErr := relayAdmission(output, limits)
	if initErr != nil {
		listener.Close()
		return "", "", nil, initErr
	}
	handler := relayHandlerWithAdmission(target, key, token, output, http.DefaultTransport, admission)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String(), token, func() error { return errors.Join(admission.finish(), server.Close()) }, nil
}

func relayAdmission(output string, limits []*ClaudeAdmission) (*requestAdmission, error) {
	if len(limits) > 1 {
		return nil, errors.New("multiple admission configurations")
	}
	if len(limits) == 0 {
		return nil, nil
	}
	return newRequestAdmission(output, limits[0])
}

func relayHandler(target *url.URL, key, token, output string, transport http.RoundTripper) http.Handler {
	return relayHandlerWithAdmission(target, key, token, output, transport, nil)
}

func relayHandlerWithAdmission(target *url.URL, key, token, output string, transport http.RoundTripper, admission *requestAdmission) http.Handler {
	var count atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized relay request", http.StatusUnauthorized)
			return
		}
		refuse := func(message string, status int) {
			if err := admission.refuse(message); err != nil {
				http.Error(w, "cannot record admission refusal", http.StatusInternalServerError)
				return
			}
			http.Error(w, message, status)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			refuse("unsupported relay endpoint", http.StatusNotFound)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			refuse("invalid request body", http.StatusBadRequest)
			return
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
			refuse("invalid request JSON", http.StatusBadRequest)
			return
		}
		var requested string
		if err := json.Unmarshal(raw["model"], &requested); err != nil || requested != model {
			refuse("unrequested model", http.StatusBadRequest)
			return
		}
		for _, field := range []string{"models", "fallbacks"} {
			if _, ok := raw[field]; ok {
				refuse("model fallback is disabled", http.StatusBadRequest)
				return
			}
		}
		raw["provider"] = json.RawMessage(`{"only":["Z.AI"],"allow_fallbacks":false}`)
		if err := admission.prepare(raw); err != nil {
			refuse(err.Error(), http.StatusBadRequest)
			return
		}
		body, err = json.Marshal(raw)
		if err != nil {
			refuse("invalid routed request", http.StatusBadRequest)
			return
		}
		if err := admission.admit(); err != nil {
			http.Error(w, "request allowance exhausted or admission evidence unavailable", http.StatusTooManyRequests)
			return
		}
		id := count.Add(1)
		record := map[string]any{"request": id, "started_at": time.Now().UTC(), "model": model, "provider": "Z.AI", "request_bytes": len(body), "stream_complete": false}
		path := filepath.Join(output, "relay-request-"+strconv.FormatUint(id, 10)+".json")
		if err := writeRelayMetadata(path, record); err != nil {
			http.Error(w, "cannot record relay request", http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		proxy := &httputil.ReverseProxy{
			Rewrite: func(p *httputil.ProxyRequest) {
				p.SetURL(target)
				if admission != nil {
					// Headers cannot opt back into billable beta/routing extensions.
					p.Out.Header = http.Header{"Anthropic-Version": []string{"2023-06-01"}, "Accept": []string{p.In.Header.Get("Accept")}}
				}
				p.Out.Header.Set("Authorization", "Bearer "+key)
				p.Out.Header.Del("X-Api-Key")
				p.Out.Header.Del("Cookie")
				p.Out.Header.Del("Proxy-Authorization")
				p.Out.Header.Set("Content-Type", "application/json")
				p.Out.Header.Set("Content-Length", strconv.Itoa(len(body)))
			},
			Transport: transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0),
			ModifyResponse: func(resp *http.Response) error {
				record["status_code"] = resp.StatusCode
				record["headers_at"] = time.Now().UTC()
				if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
					var ids []string
					resp.Body = &generationStream{ReadCloser: resp.Body, onUnknown: func() error { record["identity_gap"] = true; return writeRelayMetadata(path, record) }, onID: func(id string) error {
						ids = append(ids, id)
						record["generation_ids"] = ids
						record["stream_complete"] = false
						return writeRelayMetadata(path, record)
					}, onStop: func() error { record["stream_complete"] = true; return writeRelayMetadata(path, record) }}
				}
				return writeRelayMetadata(path, record)
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				record["transport_error"] = true
				_ = writeRelayMetadata(path, record)
				http.Error(w, "upstream request failed; inspect receipt coverage", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	})
}

// Readers may inspect progress while IDs and completion state arrive. Replace
// the small metadata object atomically so they never read half a JSON document.
func writeRelayMetadata(path string, record any) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".relay-metadata-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encodeErr := json.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if err := errors.Join(encodeErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
