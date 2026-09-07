package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func admissionRequest(ctx context.Context, path string, payload any, target any) error {
	endpoint, token := os.Getenv("FANISI_EVAL_ADMISSION_URL"), os.Getenv("FANISI_EVAL_ADMISSION_TOKEN")
	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") || token == "" {
		return errors.New("eval-bridge requires an active local evaluation supervisor")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", endpoint+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, err := io.ReadAll(io.LimitReader(response.Body, 16384))
		if err != nil {
			return err
		}
		return fmt.Errorf("evaluation admission: %s", strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(target)
}

func evaluationBridge(ctx context.Context, args []string) (runErr error) {
	var admission dispatchAdmission
	if err := admissionRequest(ctx, "/start", struct{}{}, &admission); err != nil {
		return err
	}
	defer func() {
		message := ""
		if runErr != nil {
			message = runErr.Error()
		}
		var result any
		finishErr := admissionRequest(context.Background(), "/finish", map[string]any{"slot": admission.Slot, "error": message}, &result)
		runErr = errors.Join(runErr, finishErr)
	}()
	cfg, prompt, err := bridgeArguments(admission.Config, args)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(cfg.Workspace)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("eval-bridge workspace mismatch")
	}
	if err := writeJSON(filepath.Join(admission.Output, "controller-request.json"), map[string]any{"argv": args, "reserved_turns": admission.Config.MaxCalls, "reserved_cli_estimated_cost_usd": admission.Config.MaxCost}); err != nil {
		return err
	}
	workerCtx, cancel := context.WithDeadline(ctx, admission.Deadline)
	defer cancel()
	runErr = executeClaude(workerCtx, cfg, admission.Output, []byte(prompt), admission.Options, false)
	var terminal json.RawMessage
	found, err := claudeTerminal(filepath.Join(admission.Output, "claude-stream.jsonl"), &terminal)
	if err != nil || !found {
		return errors.Join(runErr, err, errors.New("evaluation worker has no unique terminal result"))
	}
	if _, err := fmt.Fprintln(os.Stdout, string(terminal)); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}
