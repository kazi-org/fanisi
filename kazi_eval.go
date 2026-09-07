package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// KaziEvaluation is opt-in; historical evaluation manifests remain unchanged.
type KaziEvaluation struct {
	Executable       string            `json:"executable"`
	ExecutableSHA    string            `json:"executable_sha256"`
	FanisiExecutable string            `json:"fanisi_executable"`
	MaxDispatches    int               `json:"max_dispatches"`
	Artifacts        map[string]string `json:"artifacts,omitempty"`
}

type DispatchRecord struct {
	Directory     string                        `json:"directory"`
	AdmittedAt    time.Time                     `json:"admitted_at"`
	FinishedAt    *time.Time                    `json:"finished_at"`
	ReservedTurns int                           `json:"reserved_turns"`
	ReservedCost  float64                       `json:"reserved_cli_estimated_cost_usd"`
	Error         string                        `json:"error,omitempty"`
	Artifacts     map[string]ControllerArtifact `json:"artifacts"`
}

type DispatchManifest struct {
	SchemaVersion  int              `json:"schema_version"`
	MaxDispatches  int              `json:"max_dispatches"`
	Deadline       time.Time        `json:"deadline"`
	TotalTurns     int              `json:"total_turn_allowance"`
	TotalCost      float64          `json:"total_cli_estimated_cost_allowance_usd"`
	Rejected       int              `json:"rejected_launches"`
	Dispatches     []DispatchRecord `json:"dispatches"`
	IntegrityError string           `json:"integrity_error,omitempty"`
	Notes          []string         `json:"notes"`
}

type dispatchAdmission struct {
	Slot     int           `json:"slot"`
	Config   Config        `json:"config"`
	Output   string        `json:"output"`
	Options  ClaudeOptions `json:"options"`
	Deadline time.Time     `json:"deadline"`
}

type evaluationSupervisor struct {
	mu                  sync.Mutex
	cfg                 Config
	options             ClaudeOptions
	kazi                KaziEvaluation
	output              string
	manifest            DispatchManifest
	active              int
	baseline            map[string]FileIdentity
	base                string
	artifactManifestSHA string
}

func (s *evaluationSupervisor) start() (dispatchAdmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reject := func(message string) (dispatchAdmission, error) {
		s.manifest.Rejected++
		return dispatchAdmission{}, errors.New(message)
	}
	if !time.Now().Before(s.manifest.Deadline) {
		return reject("evaluation worker deadline exhausted")
	}
	if s.active != 0 {
		return reject("concurrent evaluation dispatch refused")
	}
	if s.manifest.IntegrityError != "" {
		return reject("prior dispatch changed controller artifacts")
	}
	if len(s.manifest.Dispatches) >= s.manifest.MaxDispatches {
		return reject("cumulative dispatch allowance exhausted")
	}
	artifacts, err := controllerArtifacts(s.cfg, s.kazi, s.baseline)
	if err != nil {
		s.manifest.IntegrityError = err.Error()
		return reject(err.Error())
	}
	if err := s.publishArtifacts(artifacts); err != nil {
		return reject(err.Error())
	}
	if err := auditWorkspace(context.Background(), s.cfg, s.base, s.baseline, artifacts); err != nil {
		s.manifest.IntegrityError = err.Error()
		return reject(err.Error())
	}
	slot := len(s.manifest.Dispatches) + 1
	cfg := s.cfg
	cfg.MaxCalls = s.manifest.TotalTurns / s.manifest.MaxDispatches
	if slot == s.manifest.MaxDispatches {
		cfg.MaxCalls += s.manifest.TotalTurns % s.manifest.MaxDispatches
	}
	cfg.MaxCost = s.manifest.TotalCost / float64(s.manifest.MaxDispatches)
	dir := fmt.Sprintf("dispatch-%04d", slot)
	record := DispatchRecord{Directory: dir, AdmittedAt: time.Now().UTC(), ReservedTurns: cfg.MaxCalls, ReservedCost: cfg.MaxCost, Artifacts: artifacts}
	// Persist the reservation before launch: even setup failures consume a slot.
	s.manifest.Dispatches = append(s.manifest.Dispatches, record)
	if err := os.Mkdir(filepath.Join(s.output, dir), 0700); err != nil {
		s.manifest.Dispatches[slot-1].Error = err.Error()
		return dispatchAdmission{}, err
	}
	if err := writeJSON(filepath.Join(s.output, "dispatch-manifest.json"), s.manifest); err != nil {
		return dispatchAdmission{}, err
	}
	s.active = slot
	return dispatchAdmission{slot, cfg, filepath.Join(s.output, dir), s.options, s.manifest.Deadline}, nil
}

func (s *evaluationSupervisor) finish(slot int, runError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slot < 1 || slot != s.active {
		return errors.New("unknown or already finished evaluation dispatch")
	}
	r := &s.manifest.Dispatches[slot-1]
	now := time.Now().UTC()
	r.FinishedAt = &now
	r.Error = runError
	raw, readErr := os.ReadFile(filepath.Join(s.output, "controller-artifacts.json"))
	current, err := controllerArtifacts(s.cfg, s.kazi, s.baseline)
	if readErr != nil || digest(raw) != s.artifactManifestSHA {
		err = errors.New("controller artifact manifest changed by worker")
	}
	if err == nil {
		err = equalArtifacts(r.Artifacts, current)
	}
	if err != nil {
		s.manifest.IntegrityError = err.Error()
		r.Error = errors.Join(errors.New(runError), err).Error()
	}
	s.active = 0
	if saveErr := writeJSON(filepath.Join(s.output, "dispatch-manifest.json"), s.manifest); saveErr != nil {
		return saveErr
	}
	return err
}

func startSupervisor(ctx context.Context, s *evaluationSupervisor) (string, string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		listener.Close()
		return "", "", nil, err
	}
	token := hex.EncodeToString(tokenBytes)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/start" {
			a, err := s.start()
			if err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			if err := json.NewEncoder(w).Encode(a); err != nil {
				return
			}
			return
		}
		if r.URL.Path == "/finish" {
			var request struct {
				Slot  int    `json:"slot"`
				Error string `json:"error"`
			}
			decoder := json.NewDecoder(io.LimitReader(r.Body, 16384))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				http.Error(w, "invalid completion", 400)
				return
			}
			if err := s.finish(request.Slot, request.Error); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			fmt.Fprintln(w, "{}")
			return
		}
		http.NotFound(w, r)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	closeServer := func() { server.Close(); <-done }
	return "http://" + listener.Addr().String(), token, closeServer, nil
}

func runKazi(ctx context.Context, e Evaluation, cfg Config, output string, prompt []byte, baseline map[string]FileIdentity, execution *KaziExecution) error {
	execution.Protected = map[string]string{}
	k := *e.Kazi
	raw, err := os.ReadFile(k.Executable)
	if err != nil {
		return err
	}
	execution.Protected[k.Executable] = k.ExecutableSHA
	if digest(raw) != k.ExecutableSHA {
		return errors.New("Kazi executable differs from frozen identity")
	}
	bridge := k.FanisiExecutable
	if bridge == "" {
		bridge, err = os.Executable()
		if err != nil {
			return err
		}
	}
	bridgeBytes, err := os.ReadFile(bridge)
	if err != nil {
		return err
	}
	execution.Protected[bridge] = digest(bridgeBytes)
	wrapper := filepath.Join(output, "worker-bridge")
	if err := writeNew(wrapper, []byte("#!/bin/sh\nexec "+shellQuote(bridge)+" eval-bridge \"$@\"\n")); err != nil {
		return err
	}
	wrapperBytes, err := os.ReadFile(wrapper)
	if err != nil {
		return err
	}
	execution.Protected[wrapper] = digest(wrapperBytes)
	if err := os.Chmod(wrapper, 0700); err != nil {
		return err
	}
	cfg.MaxCost = e.ClaudeMaxEstimatedCost
	supervisor := &evaluationSupervisor{baseline: baseline, base: e.Base, cfg: cfg, kazi: k, output: output, options: ClaudeOptions{e.ClaudeMaxOutputTokens, e.ClaudeProvider}, manifest: DispatchManifest{SchemaVersion: 1, MaxDispatches: k.MaxDispatches, Deadline: time.Now().UTC().Add(time.Duration(cfg.MaxSeconds) * time.Second), TotalTurns: cfg.MaxCalls, TotalCost: cfg.MaxCost, Dispatches: []DispatchRecord{}, Notes: []string{"Direct Claude uses one session; Kazi may use up to two. Dispatch reservations are not refunded, including unsuccessful launches.", "Claude turns and CLI dollar estimates are not provider request, input-token or invoice ceilings."}}}
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(supervisor.manifest.Deadline) {
		supervisor.manifest.Deadline = deadline
	}
	if err := supervisor.publishArtifacts(map[string]ControllerArtifact{}); err != nil {
		return err
	}
	endpoint, token, closeSupervisor, err := startSupervisor(ctx, supervisor)
	if err != nil {
		return err
	}
	defer closeSupervisor()
	goal := fmt.Sprintf("id = %s\nname = %s\n[scope]\nno_integration = true\n[integration]\nmode = \"none\"\n[conventions]\nprocess_contract = false\n[harness]\nid = \"claude\"\nmodel = %s\neffort = %s\ncommand = %s\n[budget]\nmax_dispatches = %d\nmax_wall_clock_ms = %d\n[[predicate]]\nid = \"external-verifier\"\nprovider = \"custom_script\"\ndescription = %s\ncmd = %s\nargs = %s\nverdict = \"exit_zero\"\n", strconv.Quote(e.TaskID), strconv.Quote("Frozen trial "+e.TaskID), strconv.Quote(model), strconv.Quote(cfg.Reasoning), strconv.Quote(wrapper), k.MaxDispatches, cfg.MaxSeconds*1000, strconv.Quote(string(prompt)), strconv.Quote(cfg.VerifyCommand[0]), tomlStrings(cfg.VerifyCommand[1:]))
	goalPath := filepath.Join(output, "controller.goal.toml")
	if err := writeNew(goalPath, []byte(goal)); err != nil {
		return err
	}
	execution.Protected[goalPath] = digest([]byte(goal))
	workerCtx, cancel := context.WithDeadline(ctx, supervisor.manifest.Deadline)
	defer cancel()
	cmd := exec.CommandContext(workerCtx, k.Executable, "apply", goalPath, "--workspace", cfg.Workspace, "--in-place", "--integration", "none", "--json")
	cmd.Dir = cfg.Workspace
	configDir := filepath.Join(output, "controller-home")
	if err := os.Mkdir(configDir, 0700); err != nil {
		return err
	}
	for _, entry := range claudeEnvironment(os.Environ(), "", configDir) {
		if !strings.HasPrefix(entry, "KAZI_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "KAZI_DB="+filepath.Join(output, "controller.sqlite3"), "FANISI_CONTROLLER_ARTIFACT_MANIFEST="+filepath.Join(output, "controller-artifacts.json"), "FANISI_EVAL_ADMISSION_URL="+endpoint, "FANISI_EVAL_ADMISSION_TOKEN="+token)
	stdout, err := os.OpenFile(filepath.Join(output, "controller-result.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(filepath.Join(output, "controller-stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer stderr.Close()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 3 * time.Second
	runErr := cmd.Run()
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	execution.Manifest = supervisor.manifest
	execution.ArtifactManifestSHA = supervisor.artifactManifestSHA
	if len(supervisor.manifest.Dispatches) > 0 {
		execution.Artifacts = supervisor.manifest.Dispatches[len(supervisor.manifest.Dispatches)-1].Artifacts
	}
	if supervisor.active != 0 {
		supervisor.manifest.IntegrityError = "dispatch did not finish before controller termination"
		execution.Manifest = supervisor.manifest
	}
	if err := writeJSON(filepath.Join(output, "dispatch-manifest.json"), supervisor.manifest); err != nil {
		return errors.Join(runErr, err)
	}
	if supervisor.manifest.IntegrityError != "" {
		return errors.Join(runErr, errors.New(supervisor.manifest.IntegrityError))
	}
	if supervisor.manifest.Rejected > 0 {
		return errors.Join(runErr, errors.New("controller exceeded dispatch admission contract"))
	}
	if len(supervisor.manifest.Dispatches) == 0 {
		return errors.Join(runErr, errors.New("controller launched no measured worker"))
	}
	var result struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
	}
	if err := readJSON(filepath.Join(output, "controller-result.json"), &result); err != nil {
		return errors.Join(runErr, err)
	}
	if result.SchemaVersion != 2 || result.Status != "converged" {
		return errors.Join(runErr, errors.New("malformed Kazi result"))
	}
	return runErr
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func tomlStrings(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Quote(v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type KaziExecution struct {
	Manifest            DispatchManifest
	ArtifactManifestSHA string
	Protected           map[string]string
	Artifacts           map[string]ControllerArtifact
}

func (s *evaluationSupervisor) publishArtifacts(artifacts map[string]ControllerArtifact) error {
	path := filepath.Join(s.output, "controller-artifacts.json")
	if err := writeJSON(path, ControllerArtifactManifest{1, s.base, artifacts}); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s.artifactManifestSHA = digest(raw)
	return nil
}
