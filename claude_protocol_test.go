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
	for _, mode := range []string{"defaults", "bounded", "admission"} {
		t.Run(mode, func(t *testing.T) {
			bounded := mode != "defaults"
			admitted := mode == "admission"
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
				handler := relayHandler(target, "offline-provider-key", "offline-fixture-key", dir, http.DefaultTransport)
				if admitted {
					admission, err := newRequestAdmission(dir, fixtureAdmission())
					if err != nil {
						t.Fatal(err)
					}
					handler = relayHandlerWithAdmission(target, "offline-provider-key", "offline-fixture-key", dir, http.DefaultTransport, admission)
				}
				relay := httptest.NewServer(handler)
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
			if admitted {
				env = append(env, "DISABLE_PROMPT_CACHING=1")
			}
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
				evidence, _ := os.ReadFile(filepath.Join(dir, "claude-admission.json"))
				t.Fatalf("CLI never sent its request to the local endpoint; admission: %s", evidence)
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
			if admitted {
				if containsCacheControl(req) {
					t.Fatal("installed Claude retained cache_control")
				}
				provider := req["provider"].(map[string]any)
				price, ok := provider["max_price"].(map[string]any)
				if !ok || price["prompt"] != 0.15 || price["completion"] != 0.5 {
					t.Fatal("routing price ceilings missing")
				}
			}
			if bounded && req["max_tokens"] != float64(8192) {
				t.Fatal("output cap was not applied")
			}
		})
	}
}

func TestInstalledClaudeAdmissionRun(t *testing.T) {
	if os.Getenv("FANISI_CLAUDE_PROTOCOL_TEST") != "1" {
		t.Skip("explicit installed Claude offline protocol probe required")
	}
	root := t.TempDir()
	output := filepath.Join(root, "output")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "key.env")
	if err := os.WriteFile(key, []byte("OPENROUTER_API_KEY=offline-fixture-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan map[string]any, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Error(err)
		}
		select {
		case requests <- raw:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"offline fixture; no inference"}}`))
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	prior := http.DefaultTransport
	http.DefaultTransport = redirectTransport{target: target, base: prior}
	defer func() { http.DefaultTransport = prior }()
	cfg := Config{Workspace: root, KeyFile: key, Reasoning: "medium", MaxCalls: 1, MaxSeconds: 15, MaxCost: 1}
	options := ClaudeOptions{Provider: "Z.AI", MaxOutputTokens: 64000, Admission: fixtureAdmission()}
	if err := runClaude(context.Background(), cfg, output, []byte("Reply OK. Do not use tools."), options); err == nil {
		t.Fatal("rejecting offline API unexpectedly succeeded")
	}
	var body map[string]any
	select {
	case body = <-requests:
	default:
		t.Fatal("installed Claude did not reach local upstream through admission")
	}
	if body["max_tokens"] != float64(8192) || containsCacheControl(body) {
		t.Fatalf("run boundary failed output/cache admission: output=%v cache=%v", body["max_tokens"], containsCacheControl(body))
	}
	provider, ok := body["provider"].(map[string]any)
	if !ok {
		t.Fatal("missing provider routing")
	}
	price, ok := provider["max_price"].(map[string]any)
	if !ok || price["prompt"] != 0.15 || price["completion"] != 0.5 || provider["allow_fallbacks"] != false {
		t.Fatal("price/provider contract missing")
	}
	var evidence AdmissionEvidence
	if err := readJSON(filepath.Join(output, "claude-admission.json"), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Admitted < 1 || evidence.Admitted > 24 || evidence.Refused != 0 {
		t.Fatalf("incorrect request evidence: %+v", evidence)
	}
}
