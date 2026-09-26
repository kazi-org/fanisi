package main

import (
	"context"
	"time"
)

// Composition schema version (separate from task schemaVersion).
const compositionSchemaVersion = 1

// CatalogRecord is one AMSL (or local mirror) capability row. Maturity labels are
// recorded claims, not admissions Fanisi may promote.
type CatalogRecord struct {
	ID          string            `json:"id"`
	Revision    string            `json:"revision"`
	Family      string            `json:"family"`
	Summary     string            `json:"summary"`
	Maturity    string            `json:"maturity"` // recorded label only
	Interfaces  []string          `json:"interfaces,omitempty"`
	Guarantees  []string          `json:"guarantees,omitempty"`
	NonGoals    []string          `json:"non_goals,omitempty"`
	EvidenceIDs []string          `json:"evidence_ids,omitempty"`
	SourcePath  string            `json:"source_path,omitempty"` // relative within catalog root
	Extra       map[string]string `json:"extra,omitempty"`
}

type CatalogIndex struct {
	SchemaVersion int             `json:"schema_version"`
	Root          string          `json:"root"`         // absolute after load
	Pin           string          `json:"pin"`          // digest of sorted record bytes or declared pin file
	SourceLabel   string          `json:"source_label"` // operator-declared origin string
	Records       []CatalogRecord `json:"records"`
}

type EvidenceKind string

const (
	EvidenceA EvidenceKind = "A" // two independent real implementations
	EvidenceB EvidenceKind = "B" // one implementation, two independent real products
	EvidenceC EvidenceKind = "C" // mature impl + immediate second real consumer
)

type EvidenceRef struct {
	ID                      string            `json:"id"`
	Kind                    EvidenceKind      `json:"kind,omitempty"` // required only for propose_amsl
	ProductIDs              []string          `json:"product_ids,omitempty"`
	ImplementationIDs       []string          `json:"implementation_ids,omitempty"`
	ProductKinds            map[string]string `json:"product_kinds,omitempty"` // real|fixture|worktree|hypothetical, operator-attested
	MatureImplementationIDs []string          `json:"mature_implementation_ids,omitempty"`
	SourceRefs              []string          `json:"source_refs,omitempty"`
	NotesRef                string            `json:"notes_ref,omitempty"` // path or note id; not authority
}

type Verdict string

const (
	VerdictReuseReference Verdict = "reuse_reference"
	VerdictAdaptLocal     Verdict = "adapt_local"
	VerdictProductLocal   Verdict = "product_local"
	VerdictProposeAMSL    Verdict = "propose_amsl"
	VerdictDefer          Verdict = "defer"
)

type CompositionRequest struct {
	SchemaVersion   int               `json:"schema_version"`
	RequestID       string            `json:"request_id"`
	ParentID        string            `json:"parent_id"`
	ProductRevision string            `json:"product_revision"`
	CatalogPin      string            `json:"catalog_pin"`
	Requirements    []string          `json:"requirements"` // full mandatory text lines/sections
	Behaviors       []BehaviorNeed    `json:"behaviors"`
	Workspace       string            `json:"workspace"`
	ReadPaths       []string          `json:"read_paths,omitempty"`
	WritePaths      []string          `json:"write_paths"`
	ProtectedPaths  []string          `json:"protected_paths"`
	AuthorityRef    string            `json:"authority_ref"` // local operator doc id; not remote auth
	DeadlineRFC3339 string            `json:"deadline"`
	Budget          CompositionBudget `json:"budget"`
	ContentSHA256   string            `json:"content_sha256,omitempty"` // filled on validate/write
}

type BehaviorNeed struct {
	BehaviorID string `json:"behavior_id"`
	Required   bool   `json:"required"`
	Contract   string `json:"contract"` // frozen interface/contract text or path ref
}

type CompositionBudget struct {
	MaxAttempts     int     `json:"max_attempts"`
	MaxSeconds      int     `json:"max_seconds"`
	MaxEstimatedUSD float64 `json:"max_estimated_usd"`
	ReservedUSD     float64 `json:"reserved_usd"`
	CurrencyNote    string  `json:"currency_note"` // e.g. "admission estimate; provider receipts separate"
}

type ComponentDecision struct {
	SchemaVersion    int                `json:"schema_version"`
	DecisionID       string             `json:"decision_id"`
	RequestHash      string             `json:"request_hash"`
	BehaviorID       string             `json:"behavior_id"`
	CandidateIDs     []string           `json:"candidate_ids,omitempty"`
	CandidateRevs    []string           `json:"candidate_revisions,omitempty"`
	EvidenceIDs      []string           `json:"evidence_ids,omitempty"`
	Verdict          Verdict            `json:"verdict"`
	Guarantees       []string           `json:"guarantees,omitempty"`
	NonGoals         []string           `json:"non_goals,omitempty"`
	Owner            string             `json:"owner"`
	CompatNotes      string             `json:"compat_notes,omitempty"`
	ConsumerChecks   []string           `json:"consumer_checks,omitempty"`
	ReasoningNoteRef string             `json:"reasoning_note_ref,omitempty"`
	Provenance       DecisionProvenance `json:"provenance"`
	ContentSHA256    string             `json:"content_sha256,omitempty"`
}

type DecisionProvenance struct {
	Source     string `json:"source"` // "operator" | "import" | "selector_glm" | "shadow_jev"
	Author     string `json:"author"`
	ImportedAt string `json:"imported_at,omitempty"` // RFC3339
	Model      string `json:"model,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

type Binding struct {
	BehaviorID   string `json:"behavior_id"`
	DecisionHash string `json:"decision_hash"`
	Kind         string `json:"kind"`       // "reuse" | "adapt" | "local_planned" | "local_implemented" | "fallback"
	TargetRef    string `json:"target_ref"` // package/path/contract id
	CatalogID    string `json:"catalog_id,omitempty"`
	CatalogRev   string `json:"catalog_revision,omitempty"`
}

type ManifestEdge struct {
	From string `json:"from"` // behavior_id
	To   string `json:"to"`
}

type ApplicationManifest struct {
	SchemaVersion  int            `json:"schema_version"`
	ManifestID     string         `json:"manifest_id"`
	RequestHash    string         `json:"request_hash"`
	DecisionHashes []string       `json:"decision_hashes"`
	Bindings       []Binding      `json:"bindings"`
	Edges          []ManifestEdge `json:"dependency_edges"`
	LocalPolicies  []string       `json:"local_policies,omitempty"`
	SecretRefs     []string       `json:"secret_refs,omitempty"` // names only; never values
	StateOwner     string         `json:"state_owner,omitempty"`
	MigrationNotes string         `json:"migration_notes,omitempty"`
	DeliveryRecipe string         `json:"delivery_recipe,omitempty"`
	RollbackLimits string         `json:"rollback_limits,omitempty"`
	ContentSHA256  string         `json:"content_sha256,omitempty"`
}

type CompositionBundle struct {
	Request   CompositionRequest
	Catalog   CatalogIndex
	Evidence  map[string]EvidenceRef
	Decisions []ComponentDecision
	Manifest  ApplicationManifest
}

type AttemptState string

const (
	AttemptProposed              AttemptState = "proposed"
	AttemptValidated             AttemptState = "validated"
	AttemptDispatched            AttemptState = "dispatched"
	AttemptRunning               AttemptState = "running"
	AttemptFailedTerminal        AttemptState = "failed_terminal"
	AttemptBlockedUncertain      AttemptState = "blocked_uncertain"
	AttemptVerifiedPendingReview AttemptState = "verified_pending_review"
	AttemptAccepted              AttemptState = "accepted"
	AttemptRejected              AttemptState = "rejected"
)

type ImplementationRequest struct {
	SchemaVersion   int      `json:"schema_version"`
	AttemptID       string   `json:"attempt_id"`
	ParentID        string   `json:"parent_id"`
	ManifestHash    string   `json:"manifest_hash"`
	RequestHash     string   `json:"request_hash"`
	FenceToken      string   `json:"fence_token"`
	Owner           string   `json:"owner"`
	ReadPaths       []string `json:"read_paths,omitempty"`
	WritePaths      []string `json:"write_paths"`
	ProtectedPaths  []string `json:"protected_paths"`
	VerifyCommand   []string `json:"verify_command"`
	DelegateArgv    []string `json:"delegate_argv"` // absolute or PATH executable + args; no shell interpolation
	Workspace       string   `json:"workspace"`
	OutputDir       string   `json:"output_dir"`
	ReservedUSD     float64  `json:"reserved_usd"`
	MaxSeconds      int      `json:"max_seconds"`
	EnvNames        []string `json:"env_names,omitempty"`
	DeadlineRFC3339 string   `json:"deadline"`
	IdempotencyKey  string   `json:"idempotency_key"` // parent|request_hash|intent
	ContentSHA256   string   `json:"content_sha256,omitempty"`
}

type UsageKnown struct {
	KnownCostUSD  *float64 `json:"known_cost_usd"` // nil => unknown
	KnownTokens   *int     `json:"known_tokens"`
	UsageComplete bool     `json:"usage_complete"`
}

type AttemptResult struct {
	SchemaVersion     int               `json:"schema_version"`
	AttemptID         string            `json:"attempt_id"`
	FenceToken        string            `json:"fence_token"`
	SourceHashes      map[string]string `json:"source_hashes"`
	ProtectedHashes   map[string]string `json:"protected_hashes"`
	FinalWriteHashes  map[string]string `json:"final_write_hashes"`
	VerifierExit      int               `json:"verifier_exit"`
	VerifierLogSHA    string            `json:"verifier_log_sha256"`
	State             AttemptState      `json:"state"`
	Usage             UsageKnown        `json:"usage"`
	UnresolvedEffects bool              `json:"unresolved_effects"`
	Error             string            `json:"error,omitempty"`
	ReviewRefs        []string          `json:"review_refs,omitempty"`
	ContentSHA256     string            `json:"content_sha256,omitempty"`
}

type JournalEvent struct {
	SchemaVersion int          `json:"schema_version"`
	Seq           uint64       `json:"seq"`
	At            time.Time    `json:"at"`
	AttemptID     string       `json:"attempt_id"`
	FenceToken    string       `json:"fence_token"`
	FromState     AttemptState `json:"from_state,omitempty"`
	ToState       AttemptState `json:"to_state"`
	Actor         string       `json:"actor"` // local operator or "fanisi-delegate"
	PayloadSHA    string       `json:"payload_sha256,omitempty"`
	Note          string       `json:"note,omitempty"`
}

type Journal struct {
	Dir string // contains lock, events/NNNNNN.json, attempts/<id>/...
}

type CompositionReview struct {
	SchemaVersion int       `json:"schema_version"`
	At            time.Time `json:"at"`
	AttemptID     string    `json:"attempt_id"`
	Decision      string    `json:"decision"` // accept|reject
	Reviewer      string    `json:"reviewer"`
	Kind          string    `json:"kind"` // human|agent
	ResultHash    string    `json:"result_sha256"`
	Notes         string    `json:"notes"`
}

type DelegateConfig struct {
	Argv       []string // from ImplementationRequest.DelegateArgv
	Workspace  string
	OutputDir  string
	MaxSeconds int
	EnvNames   []string
}

type DecisionBackendKind string

const (
	BackendCommand   DecisionBackendKind = "command"    // argv returns JSON ComponentDecision or finite choice
	BackendImport    DecisionBackendKind = "import"     // read file authored elsewhere
	BackendGLM       DecisionBackendKind = "glm"        // optional; existing pin only; small finite choice
	BackendJevShadow DecisionBackendKind = "jev_shadow" // optional; never activates into dispatch path
)

type FiniteChoiceRequest struct {
	Question      string            `json:"question"` // actual classification question
	State         string            `json:"state"`    // bounded task/requirement context
	Evidence      map[string]string `json:"evidence"` // supplied known ID -> evidence text
	SchemaVersion int               `json:"schema_version"`
	QuestionID    string            `json:"question_id"`
	Candidates    map[string]string `json:"candidates"` // id -> label
	AbstainID     string            `json:"abstain_id"`
	EvidenceIDs   []string          `json:"evidence_ids"`
}

type FiniteChoiceResult struct {
	SchemaVersion int                 `json:"schema_version"`
	SelectedID    string              `json:"selected_id"`
	Abstain       bool                `json:"abstain"`
	ReasonIDs     []string            `json:"reason_ids,omitempty"`
	Backend       DecisionBackendKind `json:"backend"`
	Model         string              `json:"model,omitempty"`
	Provider      string              `json:"provider,omitempty"`
	Confidence    *float64            `json:"confidence,omitempty"` // never grants authority
}

type DecisionBackend interface {
	Choose(ctx context.Context, req FiniteChoiceRequest) (FiniteChoiceResult, error)
}
