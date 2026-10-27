// Package managementlog records bounded management facts in the business
// transaction and exposes administrator-only retained history.
package managementlog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Source string

const (
	SourceWeb            Source = "web"
	SourceCallerKey      Source = "caller_key"
	SourceAIManual       Source = "ai_manual"
	SourceAIAuto         Source = "ai_auto"
	SourceMainstreamRule Source = "mainstream_rule"
)

type Outcome string

const (
	Succeeded Outcome = "succeeded"
	Failed    Outcome = "failed"
	Rejected  Outcome = "rejected"
	NoChange  Outcome = "no_change"
	Partial   Outcome = "partial"
	Accepted  Outcome = "accepted"
	Unknown   Outcome = "unknown"
)

var (
	ErrForbidden = errors.New("management history is restricted to administrators")
	ErrInvalid   = errors.New("invalid management history request")
	ErrNotFound  = errors.New("management history not found")
	ErrBudget    = errors.New("management history query exceeded its time budget; narrow the filters")
	ErrCleaning  = errors.New("management operation is being cleared")
)

type Subject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}
type Metadata struct {
	EventID          string  `json:"event_id"`
	CreatedAt        int64   `json:"created_at"`
	Source           Source  `json:"source"`
	ExecutorKind     string  `json:"executor_kind"`
	Actor            Subject `json:"actor"`
	Authorizer       Subject `json:"authorizer"`
	AuthorizationRef string  `json:"authorization_ref,omitempty"`
	Domain           string  `json:"domain"`
	Action           string  `json:"action"`
	CorrelationKind  string  `json:"correlation_kind,omitempty"`
	CorrelationID    string  `json:"correlation_id,omitempty"`
	Summary          string  `json:"summary"`
}

// Change contains a field name and an operation, never its raw value.
type Change struct {
	Field     string `json:"field"`
	Operation string `json:"operation"`
}
type Item struct {
	Ordinal    int64    `json:"ordinal,string"`
	Action     string   `json:"action"`
	TargetKind string   `json:"target_kind"`
	TargetID   string   `json:"target_id"`
	TargetName string   `json:"target_name"`
	Outcome    Outcome  `json:"outcome"`
	Code       string   `json:"code"`
	Changes    []Change `json:"changes"`
}
type Result struct {
	Outcome Outcome `json:"outcome"`
	Code    string  `json:"code"`
	Items   []Item  `json:"items,omitempty"`
}
type Record struct {
	Metadata
	Result
}
type Operation struct {
	ID int64 `json:"id,string"`
	Metadata
	Outcome   Outcome `json:"outcome"`
	Code      string  `json:"code"`
	Targets   int64   `json:"targets,string"`
	Succeeded int64   `json:"succeeded,string"`
	Skipped   int64   `json:"skipped,string"`
	Failed    int64   `json:"failed,string"`
	Cleaning  bool    `json:"cleaning"`
}
type contextKey struct{}
type operationState struct {
	mu       sync.Mutex
	metadata Metadata
	result   Result
	replay   bool
	attempts []Record
}

// WithOperation is called only after the server establishes identity and action.
// Metadata must never be decoded from a public request or model argument.
func WithOperation(ctx context.Context, metadata Metadata) context.Context {
	if metadata.EventID == "" {
		metadata.EventID = newEventID()
	}
	if metadata.CreatedAt == 0 {
		metadata.CreatedAt = time.Now().UnixMilli()
	}
	return context.WithValue(ctx, contextKey{}, &operationState{metadata: metadata})
}
func newEventID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
func stateFrom(ctx context.Context) *operationState {
	state, _ := ctx.Value(contextKey{}).(*operationState)
	return state
}
func MetadataFromContext(ctx context.Context) (Metadata, bool) {
	state := stateFrom(ctx)
	if state == nil {
		return Metadata{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.metadata, true
}
func SetResult(ctx context.Context, result Result) {
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		if result.Items == nil && result.Outcome != "" {
			result.Items = cloneItems(state.result.Items)
			for i := range result.Items {
				result.Items[i].Outcome = result.Outcome
			}
		} else {
			result.Items = cloneItems(result.Items)
		}
		state.result = result
	}
}
func MarkReplay(ctx context.Context) {
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		state.replay = true
		state.mu.Unlock()
	}
}
func SetCorrelation(ctx context.Context, kind, id string) {
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		state.metadata.CorrelationKind = kind
		state.metadata.CorrelationID = id
		state.mu.Unlock()
	}
}
func AddItems(ctx context.Context, items ...Item) {
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		state.result.Items = append(state.result.Items, cloneItems(items)...)
		state.mu.Unlock()
	}
}

// RecordContextTx is called at a successful business write boundary. It is a
// no-op for ordinary requests and replay, and must run before committing.
func RecordContextTx(ctx context.Context, tx *sql.Tx) error {
	state := stateFrom(ctx)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	if state.replay || state.metadata.Domain == "" {
		state.mu.Unlock()
		return nil
	}
	value := Record{Metadata: state.metadata, Result: state.result}
	value.Items = cloneItems(value.Items)
	state.mu.Unlock()
	if value.Outcome == "" {
		value.Outcome = Succeeded
	}
	_, err := RecordTx(ctx, tx, value)
	return err
}

// Commit couples a direct business transaction to its management record.
func Commit(ctx context.Context, tx *sql.Tx) error {
	if err := RecordContextTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetItemsResult derives a batch outcome from actual per-item domain decisions.
func SetItemsResult(ctx context.Context, items []Item) {
	success, skipped, failed := counts(items)
	outcome := Succeeded
	unknown, partial, rejected := false, false, true
	for _, item := range items {
		unknown = unknown || item.Outcome == Unknown
		partial = partial || item.Outcome == Partial
		rejected = rejected && item.Outcome == Rejected
	}
	switch {
	case unknown && success == 0 && failed == 0:
		outcome = Unknown
	case unknown || partial:
		outcome = Partial
	case rejected && len(items) > 0:
		outcome = Rejected
	case failed > 0 && success > 0:
		outcome = Partial
	case failed > 0:
		outcome = Failed
	case success == 0 && skipped > 0:
		outcome = NoChange
	case success > 0 && skipped > 0:
		outcome = Partial
	}
	SetResult(ctx, Result{Outcome: outcome, Items: items})
}

// TrackInternalAttempt lets a wrapped submission record a rule failure only
// after its outer transaction has rolled back.
func TrackInternalAttempt(ctx context.Context, value Record) Record {
	if value.EventID == "" {
		value.EventID = newEventID()
	}
	if value.CreatedAt == 0 {
		value.CreatedAt = nowUnix()
	}
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		state.attempts = append(state.attempts, value)
		state.mu.Unlock()
	}
	return value
}
func WithoutOperation(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, (*operationState)(nil))
}
func OutcomeFromContext(ctx context.Context) Outcome {
	state := stateFrom(ctx)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.result.Outcome
}

func cloneItems(items []Item) []Item {
	out := append([]Item(nil), items...)
	for i := range out {
		out[i].Changes = append([]Change(nil), out[i].Changes...)
	}
	return out
}
func SetAction(ctx context.Context, action, summary string) {
	if state := stateFrom(ctx); state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
		state.metadata.Action = action
		state.metadata.Summary = summary
		for i := range state.result.Items {
			state.result.Items[i].Action = action
		}
	}
}
