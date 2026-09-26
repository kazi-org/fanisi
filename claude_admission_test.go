package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureAdmission() *ClaudeAdmission {
	return &ClaudeAdmission{MaxRequests: 24, MaxOutputTokens: 8192, MaxPrice: AdmissionPrice{Prompt: 0.15, Completion: 0.50}}
}

func TestAdmissionConcurrentRetriesNeverExceedUpstreamAllowance(t *testing.T) {
	dir := t.TempDir()
	limits := fixtureAdmission()
	admission, err := newRequestAdmission(dir, limits)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	server := httptest.NewServer(relayHandlerWithAdmission(target, "key", "token", dir, http.DefaultTransport, admission))
	defer server.Close()
	var wg sync.WaitGroup
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/messages", bytes.NewBufferString(`{"model":"`+model+`","max_tokens":8192,"messages":[]}`))
			request.Header.Set("Authorization", "Bearer token")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}()
	}
	wg.Wait()
	if calls.Load() != 24 {
		t.Fatalf("forwarded %d requests", calls.Load())
	}
	var evidence AdmissionEvidence
	if err := readJSON(filepath.Join(dir, "claude-admission.json"), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Admitted != 24 || evidence.Refused != 24 || evidence.Reasons["request_allowance_exhausted"] != 24 {
		t.Fatalf("admission reset or failed requests refunded: %+v", evidence)
	}
}

func TestAdmissionRejectsExtensionsAndPinsRouting(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		tokens      int
		pass        bool
	}{
		{"routing_override", `,"provider":{"only":["other"],"allow_fallbacks":true,"max_price":{"prompt":100}}`, 8192, true},
		{"output_override", "", 8193, false},
		{"missing_output", "", 0, false},
		{"plugin", `,"plugins":[{"id":"web"}]`, 8192, false},
		{"service_tier", `,"service_tier":"priority"`, 8192, false},
		{"server_tool", `,"tools":[{"type":"web_search_20250305","name":"web_search"}]`, 8192, false},
		{"cache_write", `,"system":[{"type":"text","text":"fixture","cache_control":{"type":"ephemeral"}}]`, 8192, false},
		{"keep_all_context", `,"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`, 8192, true},
		{"server_compaction", `,"context_management":{"edits":[{"type":"compact_20260112"}]}`, 8192, false},
		{"custom_tool", `,"tools":[{"name":"Read","input_schema":{"type":"object"}}]`, 8192, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			admission, err := newRequestAdmission(dir, fixtureAdmission())
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				provider := body["provider"].(map[string]any)
				price := provider["max_price"].(map[string]any)
				if provider["allow_fallbacks"] != false || provider["only"].([]any)[0] != "Z.AI" || price["prompt"] != 0.15 || price["completion"] != 0.5 {
					t.Errorf("routing override escaped: %v", body)
				}
				if r.Header.Get("Anthropic-Beta") != "" || r.Header.Get("X-OpenRouter-Provider") != "" {
					t.Error("extension header forwarded")
				}
				w.WriteHeader(403)
			}))
			defer upstream.Close()
			target, _ := url.Parse(upstream.URL)
			handler := relayHandlerWithAdmission(target, "key", "token", dir, http.DefaultTransport, admission)
			raw := map[string]any{"model": model, "max_tokens": tc.tokens, "messages": []any{}}
			base, _ := json.Marshal(raw)
			body := append(base[:len(base)-1], []byte(tc.extra+"}")...)
			request := httptest.NewRequest(http.MethodPost, "http://localhost/v1/messages", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer token")
			request.Header.Set("Anthropic-Beta", "unknown-paid-extension")
			request.Header.Set("X-OpenRouter-Provider", "other")
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if (calls == 1) != tc.pass {
				t.Fatalf("upstream calls=%d", calls)
			}
			var evidence AdmissionEvidence
			if err := readJSON(filepath.Join(dir, "claude-admission.json"), &evidence); err != nil {
				t.Fatal(err)
			}
			if !tc.pass && (evidence.Refused != 1 || evidence.Admitted != 0) {
				t.Fatalf("refusal missing: %+v", evidence)
			}
		})
	}
}

func TestAdmissionSharesFrozenTotalAcrossKaziDispatches(t *testing.T) {
	for _, arm := range []string{"claude", "kazi-claude"} {
		t.Run(arm, func(t *testing.T) {
			e, path := kaziFixture(t, "two")
			e.ClaudeAdmission = fixtureAdmission()
			if err := writeJSON(path, e); err != nil {
				t.Fatal(err)
			}
			if err := evalRun(context.Background(), path, arm, 1); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(e.Output, e.TaskID, arm, "1")
			dirs := []string{root}
			if arm == "kazi-claude" {
				var manifest DispatchManifest
				if err := readJSON(filepath.Join(root, "dispatch-manifest.json"), &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest.TotalRequests != 24 || len(manifest.Dispatches) != 2 || manifest.Dispatches[0].Error == "" {
					t.Fatalf("failed dispatch omitted: %+v", manifest)
				}
				dirs = nil
				for _, dispatch := range manifest.Dispatches {
					if dispatch.ReservedRequests != 12 {
						t.Fatalf("request allowance reset: %+v", manifest)
					}
					dirs = append(dirs, filepath.Join(root, dispatch.Directory))
				}
			}
			total := 0
			for _, dir := range dirs {
				var evidence AdmissionEvidence
				if err := readJSON(filepath.Join(dir, "claude-admission.json"), &evidence); err != nil {
					t.Fatal(err)
				}
				total += evidence.Limits.MaxRequests
				if evidence.Admitted != 0 {
					t.Fatal("fake worker unexpectedly made provider request")
				}
			}
			if total != 24 {
				t.Fatalf("unequal request total: %d", total)
			}
		})
	}
}

func TestAdmissionEvidenceFailureCannotForward(t *testing.T) {
	dir := t.TempDir()
	admission, err := newRequestAdmission(dir, fixtureAdmission())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := admission.admit(); err == nil {
		t.Fatal("lost evidence accepted")
	}
}

func TestAdmissionAbsentPreservesLegacyRelay(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !containsCacheControl(body) {
			t.Error("legacy cache controls changed")
		}
		if body["max_tokens"] != float64(32000) {
			t.Error("legacy output changed")
		}
		if _, ok := body["provider"].(map[string]any)["max_price"]; ok {
			t.Error("absent block added price cap")
		}
		w.WriteHeader(403)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	handler := relayHandler(target, "key", "token", dir, http.DefaultTransport)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/v1/messages", bytes.NewBufferString(`{"model":"`+model+`","max_tokens":32000,"system":[{"type":"text","text":"fixture","cache_control":{"type":"ephemeral"}}]}`))
	request.Header.Set("Authorization", "Bearer token")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if calls != 1 {
		t.Fatal("legacy request not forwarded")
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-admission.json")); !os.IsNotExist(err) {
		t.Fatal("absent block created admission ledger")
	}
}

func TestInstalledAdmissionBudget(t *testing.T) {
	binary, controller := os.Getenv("FANISI_E77_CANDIDATE_BINARY"), os.Getenv("FANISI_E77_KAZI_BINARY")
	if binary == "" || controller == "" {
		t.Skip("explicit isolated Fanisi/Kazi binaries required")
	}
	for _, variant := range []string{"claude", "kazi-claude", "legacy_bridge"} {
		t.Run(variant, func(t *testing.T) {
			arm := variant
			if variant == "legacy_bridge" {
				arm = "kazi-claude"
				if os.Getenv("FANISI_E77_LEGACY_BINARY") == "" {
					t.Skip("explicit old bridge binary required")
				}
			}
			e, path := kaziFixture(t, "admission")
			raw, err := os.ReadFile(controller)
			if err != nil {
				t.Fatal(err)
			}
			bin := filepath.Dir(e.Kazi.Executable)
			e.Kazi.Executable = controller
			e.Kazi.ExecutableSHA = digest(raw)
			e.Kazi.FanisiExecutable = binary
			if variant == "legacy_bridge" {
				e.Kazi.FanisiExecutable = os.Getenv("FANISI_E77_LEGACY_BINARY")
			}
			e.ClaudeAdmission = fixtureAdmission()
			worker := `#!/bin/sh
cat >/dev/null
count=25
case "$HOME" in *dispatch-*) count=13;; esac
i=0
while [ "$i" -lt "$count" ]; do
 curl --noproxy '*' -s -H "Authorization: Bearer $ANTHROPIC_AUTH_TOKEN" -H 'Content-Type: application/json' -d '{"model":"z-ai/glm-5.3-flash","max_tokens":8192,"messages":[{"role":"user","content":"offline fixture"}]}' "$ANTHROPIC_BASE_URL/v1/messages" >/dev/null || exit 9
 i=$((i+1))
done
case "$HOME" in *dispatch-0001*) exit 7;; esac
printf 'new\n' > value.txt
printf '{"type":"result","is_error":false}\n'
`
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(worker), 0700); err != nil {
				t.Fatal(err)
			}
			var cfg Config
			if err := readJSON(e.TaskConfig, &cfg); err != nil {
				t.Fatal(err)
			}
			cfg.MaxSeconds = 30
			if err := writeJSON(e.TaskConfig, cfg); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(path, e); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			// The proxy rejects CONNECT before any TLS/provider connection. A transport
			// failure still consumes admission; this never contacts a provider endpoint.
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect {
					t.Error("unexpected proxy request")
				}
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer proxy.Close()
			t.Setenv("HTTPS_PROXY", proxy.URL)
			t.Setenv("NO_PROXY", "localhost,127.0.0.1")
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, binary, "eval", "--config", path, "--arm", arm, "--attempt", "1").CombinedOutput()
			if variant == "legacy_bridge" {
				if err == nil || calls.Load() != 0 {
					t.Fatalf("old bridge escaped admission handshake: err=%v forwarded=%d", err, calls.Load())
				}
				return
			}
			if err != nil && arm == "claude" {
				t.Fatalf("installed admission run: %v %s", err, out)
			}
			if err != nil {
				t.Logf("controller execution ended with %v; independently checking final candidate", err)
			}
			if calls.Load() != 24 {
				t.Fatalf("installed run forwarded %d attempts, want24", calls.Load())
			}
			root := filepath.Join(e.Output, e.TaskID, arm, "1")
			var attempt Attempt
			if err := readJSON(filepath.Join(root, "attempt.json"), &attempt); err != nil {
				t.Fatal(err)
			}
			if attempt.Status != "verified_pending_review" {
				t.Fatal("independent lifecycle missing")
			}
			dirs := []string{root}
			wantEach := 24
			if arm == "kazi-claude" {
				var manifest DispatchManifest
				if err := readJSON(filepath.Join(root, "dispatch-manifest.json"), &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest.TotalRequests != 24 || len(manifest.Dispatches) != 2 || manifest.Dispatches[0].Error == "" {
					t.Fatal("failed request/dispatch reservation missing")
				}
				dirs = nil
				wantEach = 12
				for _, dispatch := range manifest.Dispatches {
					if dispatch.ReservedRequests != 12 {
						t.Fatal("dispatch reservation reset")
					}
					dirs = append(dirs, filepath.Join(root, dispatch.Directory))
				}
			}
			for _, dir := range dirs {
				var evidence AdmissionEvidence
				if err := readJSON(filepath.Join(dir, "claude-admission.json"), &evidence); err != nil {
					t.Fatal(err)
				}
				if evidence.Admitted != wantEach || evidence.Refused != 1 || evidence.Limits.MaxRequests != wantEach {
					t.Fatalf("wrong admission evidence: %+v", evidence)
				}
			}
		})
	}
}

func TestAdmissionRequiresBridgeCapabilityBeforeLaunch(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_without_block", true: "legacy_with_block"}[enabled], func(t *testing.T) {
			e, _ := kaziFixture(t, "success")
			var cfg Config
			if err := readJSON(e.TaskConfig, &cfg); err != nil {
				t.Fatal(err)
			}
			baseline, err := workspaceInventory(e.Repository)
			if err != nil {
				t.Fatal(err)
			}
			s := &evaluationSupervisor{cfg: cfg, baseline: baseline, base: e.Base, output: t.TempDir(), manifest: DispatchManifest{SchemaVersion: 1, MaxDispatches: 2, Deadline: time.Now().Add(time.Minute), TotalTurns: 5, TotalCost: 2, TotalRequests: 24, Dispatches: []DispatchRecord{}}}
			if enabled {
				s.options.Admission = fixtureAdmission()
			}
			endpoint, token, closeServer, err := startSupervisor(context.Background(), s)
			if err != nil {
				t.Fatal(err)
			}
			defer closeServer()
			request, _ := http.NewRequest(http.MethodPost, endpoint+"/start", bytes.NewBufferString(`{}`))
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if enabled && (response.StatusCode != 409 || len(s.manifest.Dispatches) != 0) {
				t.Fatal("old bridge admitted before capability validation")
			}
			if !enabled && (response.StatusCode != 200 || len(s.manifest.Dispatches) != 1) {
				t.Fatal("absent block broke legacy bridge")
			}
		})
	}
}
