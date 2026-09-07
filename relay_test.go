package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRelayPinsProviderAndStreamsWithoutExposingKey(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/messages" || r.URL.RawQuery != "beta=true" {
			t.Errorf("wrong upstream URL: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer provider-secret" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Cookie") != "" {
			t.Error("incorrect upstream credential boundary")
		}
		var body struct {
			Model    string
			Provider struct {
				Only           []string
				AllowFallbacks bool `json:"allow_fallbacks"`
			}
			Messages []any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != model || len(body.Provider.Only) != 1 || body.Provider.Only[0] != "Z.AI" || body.Provider.AllowFallbacks || len(body.Messages) != 1 {
			t.Errorf("request contract changed: %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"message_start","message":{"id":"gen-early"}}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = io.WriteString(w, "data: final\n\n")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL + "/api")
	output := t.TempDir()
	relay := httptest.NewServer(relayHandler(target, "provider-secret", "local-secret", output, http.DefaultTransport))
	defer relay.Close()
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"task"}],"stream":true,"provider":{"only":["other"],"allow_fallbacks":true}}`
	req, _ := http.NewRequest(http.MethodPost, relay.URL+"/v1/messages?beta=true", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-secret")
	req.Header.Set("X-Api-Key", "untrusted")
	req.Header.Set("Cookie", "untrusted")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	first, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || first != `data: {"type":"message_start","message":{"id":"gen-early"}}`+"\n" {
		t.Fatalf("stream was not forwarded: %q %v", first, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected upstream call count")
	}
	record, err := os.ReadFile(filepath.Join(output, "relay-request-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(record), "gen-early") {
		t.Fatal("stream identity was lost before response completion")
	}
	if strings.Contains(string(record), "secret") {
		t.Fatal("credential leaked into request metadata")
	}
}

func TestRelayRejectsUnauthorizedOrUnrequestedCallsBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	relay := httptest.NewServer(relayHandler(target, "provider-secret", "local-secret", t.TempDir(), http.DefaultTransport))
	defer relay.Close()
	for _, tc := range []struct {
		path, auth, body string
		want             int
	}{
		{"/v1/messages", "wrong", `{"model":"` + model + `"}`, 401},
		{"/other", "local-secret", `{"model":"` + model + `"}`, 404},
		{"/v1/messages", "local-secret", `{"model":"other"}`, 400},
		{"/v1/messages", "local-secret", `{"model":"` + model + `","fallbacks":[]}`, 400},
		{"/v1/messages", "local-secret", `not JSON`, 400},
	} {
		req, _ := http.NewRequest(http.MethodPost, relay.URL+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+tc.auth)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("unexpected status: %d want%d", res.StatusCode, tc.want)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected input reached provider")
	}
}

func TestRelayRetainsUnknownIdentityAsCoverageGap(t *testing.T) {
	payload := `data: {"type":"message_start","message":{"id":"gen-known"}}` + "\n\n" +
		`data: {"type":"message_start","message":{"id":"unrecognized"}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, payload)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	output := t.TempDir()
	relay := httptest.NewServer(relayHandler(target, "provider-secret", "local-secret", output, http.DefaultTransport))
	defer relay.Close()
	request, _ := http.NewRequest(http.MethodPost, relay.URL+"/v1/messages", strings.NewReader(`{"model":"`+model+`"}`))
	request.Header.Set("Authorization", "Bearer local-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil || string(got) != payload {
		t.Fatalf("forwarded stream changed: %v", err)
	}
	var record struct {
		Gap      bool     `json:"identity_gap"`
		Complete bool     `json:"stream_complete"`
		IDs      []string `json:"generation_ids"`
	}
	if err := readJSON(filepath.Join(output, "relay-request-1.json"), &record); err != nil {
		t.Fatal(err)
	}
	if !record.Gap || !record.Complete || len(record.IDs) != 1 {
		t.Fatalf("unknown identity hidden by valid identity/stop: %+v", record)
	}
}
