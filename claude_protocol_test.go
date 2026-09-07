package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in characterization of the installed CLI; the endpoint is local and
// always rejects, so this test cannot make a provider inference call.
func TestInstalledClaudeRequestShape(t *testing.T) {
	if os.Getenv("FANISI_CLAUDE_PROTOCOL_TEST") != "1" {
		t.Skip("set FANISI_CLAUDE_PROTOCOL_TEST=1 for the installed CLI's offline protocol probe")
	}
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "bounded"}[bounded], func(t *testing.T) {
			body := make(chan map[string]any, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					var raw map[string]any
					if err := json.NewDecoder(r.Body).Decode(&raw); err == nil {
						select {
						case body <- raw:
						default:
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"offline protocol probe; no inference"}}`))
			}))
			defer server.Close()
			dir := t.TempDir()
			config := filepath.Join(dir, "config")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			cfg := Config{Reasoning: "medium", MaxCost: 1, MaxCalls: 1}
			endpoint := server.URL
			if bounded {
				target, err := url.Parse(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				relay := httptest.NewServer(relayHandler(target, "offline-provider-key", "offline-fixture-key", dir, http.DefaultTransport))
				defer relay.Close()
				endpoint = relay.URL
			}
			env := claudeEnvironment(os.Environ(), "offline-fixture-key", config)
			for i, e := range env {
				if strings.HasPrefix(e, "ANTHROPIC_BASE_URL=") {
					env[i] = "ANTHROPIC_BASE_URL=" + endpoint
				}
			}
			env = append(env, "CLAUDE_CODE_MAX_RETRIES=0")
			if bounded {
				env = append(env, "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING=1", "MAX_THINKING_TOKENS=2048", "CLAUDE_CODE_MAX_OUTPUT_TOKENS=8192", "CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS=3000")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "claude", claudeArguments(cfg)...)
			cmd.Dir = dir
			cmd.Env = env
			cmd.Stdin = strings.NewReader("Reply with OK. Do not use tools.")
			if err := cmd.Run(); err == nil {
				t.Fatal("rejecting local API unexpectedly succeeded")
			}
			var req map[string]any
			select {
			case req = <-body:
			default:
				t.Fatal("CLI never sent its request to the local endpoint")
			}
			if req["model"] != model {
				t.Fatalf("unrequested model: %v", req["model"])
			}
			summary := map[string]any{"model": req["model"], "max_tokens": req["max_tokens"], "thinking": req["thinking"], "output_config": req["output_config"]}
			b, err := json.Marshal(summary)
			if err != nil {
				t.Fatal(err)
			}
			t.Log(string(b))
			if bounded {
				provider, ok := req["provider"].(map[string]any)
				if !ok || provider["allow_fallbacks"] != false {
					t.Fatal("installed CLI did not reach provider relay")
				}
			}
			if bounded && req["max_tokens"] != float64(8192) {
				t.Fatal("output cap was not applied")
			}
		})
	}
}
