package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicitly paid, opt-in transport probe. Artifacts survive failure so its
// generation receipt can be reconciled independently of the test verdict.
func TestLiveRelayCapturesEarlyGenerationID(t *testing.T) {
	if os.Getenv("FANISI_LIVE_RELAY_TEST") != "1" {
		t.Skip("opt-in paid OpenRouter transport probe")
	}
	output, keyFile := os.Getenv("FANISI_LIVE_RELAY_OUTPUT"), os.Getenv("FANISI_LIVE_RELAY_KEY_FILE")
	if output == "" || keyFile == "" {
		t.Fatal("set an explicit fresh output directory and key file")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	key, err := resolveKey(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, token, closeRelay, err := providerRelay(key, output)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRelay()
	body := `{"model":"` + model + `","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK only."}]}`
	request, err := http.NewRequest(http.MethodPost, endpoint+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var detail struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&detail)
		message := strings.ReplaceAll(strings.ReplaceAll(detail.Error.Message, key, "[redacted]"), token, "[redacted]")
		if strings.Contains(message, "sk-") || strings.Contains(message, "Bearer") {
			message = "[redacted provider error]"
		}
		t.Fatalf("provider HTTP status %d: %s; retain request metadata", response.StatusCode, message)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if strings.Contains(line, "message_start") {
			ids, idErr := generationIDs(output)
			if idErr == nil && len(ids) > 0 {
				t.Logf("captured %s before closing the streamed reply; reconcile %s", ids[0], output)
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				t.Fatal("stream ended without a retained generation identity")
			}
			t.Fatal(err)
		}
	}
}
