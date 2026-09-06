package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type redirectTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (r redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}

func testConfig(t *testing.T) Config {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "repo")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{filepath.Join(workspace, "value.txt"): "old", filepath.Join(root, "brief.md"): "Change value.txt to new. The configured verifier must pass.", filepath.Join(root, "key.env"): "OPENROUTER_API_KEY=test-secret"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return Config{SchemaVersion: 1, Workspace: workspace, Output: filepath.Join(root, "run"), PromptFile: filepath.Join(root, "brief.md"), KeyFile: filepath.Join(root, "key.env"), WritePaths: []string{"value.txt"}, VerifyCommand: []string{"sh", "-c", `test "$(cat value.txt)" = new`}, MaxCalls: 1, MaxSeconds: 30, MaxCost: 0.1, MaxTokens: 50000, MaxOutputTokens: 1024, MaxContextBytes: 20000, PacketBytes: 10000, Reasoning: "medium", Pricing: Pricing{Input: 0.075, Output: 0.25, Source: "test fixture"}}
}

func toolCall(id, name string, args any) map[string]any {
	b, err := json.Marshal(args)
	if err != nil {
		panic(err)
	} // test-only fixture
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(b)}}
}

func TestEndToEndRun(t *testing.T) {
	for _, tc := range []struct {
		name                                              string
		noEdit, missingUsage, wrongModel, editAfterVerify bool
		wantSuccess                                       bool
	}{
		{name: "edit_and_verify", wantSuccess: true},
		{name: "prose_is_not_acceptance", noEdit: true},
		{name: "missing_usage_is_not_free", missingUsage: true},
		{name: "unexpected_model", wrongModel: true},
		{name: "verification_cannot_precede_final_edit", editAfterVerify: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("wrong authorization")
				}
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if req["model"] != model {
					t.Error("unpinned request model")
				}
				provider := req["provider"].(map[string]any)
				if provider["allow_fallbacks"] != false || provider["only"].([]any)[0] != "z-ai" {
					t.Error("unpinned provider")
				}
				message := map[string]any{"role": "assistant", "content": "done"}
				if !tc.noEdit {
					calls := []any{toolCall("edit", "replace", map[string]any{"path": "value.txt", "old": "old", "new": "new"}), toolCall("check", "verify", map[string]any{})}
					if tc.editAfterVerify {
						calls = append(calls, toolCall("undo", "replace", map[string]any{"path": "value.txt", "old": "new", "new": "bad"}))
					}
					message["tool_calls"] = calls
				}
				m := model
				if tc.wrongModel {
					m = "unrequested/model"
				}
				response := map[string]any{"id": "gen-test", "model": m, "provider": "Z.AI", "choices": []any{map[string]any{"message": message, "finish_reason": "stop"}}}
				if !tc.missingUsage {
					response["usage"] = map[string]any{"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150, "cost": 0.001, "prompt_tokens_details": map[string]any{"cached_tokens": 20}, "completion_tokens_details": map[string]any{"reasoning_tokens": 10}}
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := server.Client()
			client.Transport = redirectTransport{base: client.Transport, target: target}
			err = runWithClient(context.Background(), cfg, client)
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("unexpected outcome: %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("unexpected API retries: %d", requests.Load())
			}
			var result map[string]any
			if err := readJSON(filepath.Join(cfg.Output, "result.json"), &result); err != nil {
				t.Fatal(err)
			}
			if result["verification_passed"] != tc.wantSuccess || result["source_review"] != "pending" {
				t.Fatal("incorrect completion verdict")
			}
			if tc.missingUsage && result["usage_complete"] != false {
				t.Fatal("missing usage presented as complete")
			}
			if _, err := os.Stat(filepath.Join(cfg.Workspace, ".fanisi-lock")); !os.IsNotExist(err) {
				t.Fatal("lock not released")
			}
			entries, err := os.ReadDir(cfg.Output)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				b, err := os.ReadFile(filepath.Join(cfg.Output, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), "test-secret") {
					t.Fatal("key leaked to artifacts")
				}
			}
		})
	}
}

func TestWorkspaceClaimExcludesSecondRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	release, err := claimWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := claimWorkspace(root); err == nil {
		t.Fatal("second runner acquired same workspace")
	}
}

func TestConfiguredFormatterAndSecretIsolation(t *testing.T) {
	cfg := testConfig(t)
	t.Setenv("OPENROUTER_API_KEY", "secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "other")
	if err := os.Mkdir(cfg.Output, 0700); err != nil {
		t.Fatal(err)
	}
	cfg.FormatCommand = []string{"sh", "-c", `test -z "$OPENROUTER_API_KEY$ANTHROPIC_AUTH_TOKEN" && printf formatted > value.txt`}
	h := Harness{cfg: cfg}
	call := Call{}
	call.Function.Name = "format"
	call.Function.Arguments = "{}"
	if _, _, err := h.execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cfg.Workspace, "value.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "formatted" {
		t.Fatal("configured formatter not executed")
	}
}

func TestCreateIsScopedAndNeverOverwrites(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.WritePaths = append(cfg.WritePaths, "new/file.txt")
	if err := os.Mkdir(cfg.Output, 0700); err != nil {
		t.Fatal(err)
	}
	h := Harness{cfg: cfg}
	call := Call{}
	call.Function.Name = "create"
	call.Function.Arguments = `{"path":"new/file.txt","content":"created"}`
	if _, _, err := h.execute(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.execute(context.Background(), call); err == nil {
		t.Fatal("existing file overwritten")
	}
	call.Function.Arguments = `{"path":"../escape","content":"bad"}`
	if _, _, err := h.execute(context.Background(), call); err == nil {
		t.Fatal("path escape allowed")
	}
}

func TestVerifierLogsSpoolToDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	output, code, err := runCommand(context.Background(), dir, []string{"sh", "-c", `i=0; while [ "$i" -lt 10000 ]; do printf '0123456789'; i=$((i+1)); done`}, log)
	if err != nil || code != 0 {
		t.Fatalf("command: %d %v", code, err)
	}
	st, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 100000 || len(output) > 7100 {
		t.Fatal(fmt.Sprintf("full=%d excerpt=%d", st.Size(), len(output)))
	}
}
