// Package routing builds immutable, owner-scoped logical-model snapshots.
// Snapshots contain no credential, ciphertext, secret reference, or catalog
// source row; one source-neutral binding contributes exactly one candidate.
package routing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"unicode"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var (
	ErrInvalidIdentity   = errors.New("routing: invalid model identity")
	ErrNotFound          = errors.New("routing: model not found")
	ErrAmbiguousIdentity = errors.New("routing: model identity is ambiguous")
	ErrUnbound           = errors.New("routing: model has no available bindings")
)

type Store struct {
	db *sql.DB
}

func New(store *db.Store) (*Store, error) {
	if store == nil || store.DB() == nil {
		return nil, errors.New("routing: store is required")
	}
	return &Store{db: store.DB()}, nil
}

type Identity struct {
	ModelID  string
	FullName string
}

type Candidate struct {
	bindingID        int64
	endpointID       int64
	endpointKeyID    int64
	connectorType    string
	canonicalBaseURL string
	upstreamModelID  string
	forceStoreFalse  bool
	endpointRevision int64
	keyRevision      int64
	order            int
}

func (c Candidate) BindingID() int64           { return c.bindingID }
func (c Candidate) EndpointID() int64          { return c.endpointID }
func (c Candidate) EndpointKeyID() int64       { return c.endpointKeyID }
func (c Candidate) ConnectorType() string      { return c.connectorType }
func (c Candidate) CanonicalBaseURL() string   { return c.canonicalBaseURL }
func (c Candidate) UpstreamModelID() string    { return c.upstreamModelID }
func (c Candidate) ForceStoreFalse() bool      { return c.forceStoreFalse }
func (c Candidate) EndpointRevision() int64    { return c.endpointRevision }
func (c Candidate) EndpointKeyRevision() int64 { return c.keyRevision }
func (c Candidate) Order() int                 { return c.order }
func (Candidate) String() string               { return "[redacted routing candidate]" }
func (Candidate) GoString() string             { return "[redacted routing candidate]" }
func (Candidate) LogValue() slog.Value         { return slog.StringValue("[redacted routing candidate]") }

type Snapshot struct {
	modelID          int64
	ownerUserID      int64
	provider         string
	model            string
	fullName         string
	routeStrategy    string
	silentRetry      bool
	flattenToolCalls bool
	revision         int64
	bindingRevision  int64
	candidates       []Candidate
}

func (s Snapshot) ModelID() int64          { return s.modelID }
func (s Snapshot) OwnerUserID() int64      { return s.ownerUserID }
func (s Snapshot) Provider() string        { return s.provider }
func (s Snapshot) Model() string           { return s.model }
func (s Snapshot) FullName() string        { return s.fullName }
func (s Snapshot) RouteStrategy() string   { return s.routeStrategy }
func (s Snapshot) SilentRetry() bool       { return s.silentRetry }
func (s Snapshot) FlattenToolCalls() bool  { return s.flattenToolCalls }
func (s Snapshot) Revision() int64         { return s.revision }
func (s Snapshot) BindingRevision() int64  { return s.bindingRevision }
func (s Snapshot) CandidateCount() int     { return len(s.candidates) }
func (s Snapshot) Candidates() []Candidate { return append([]Candidate(nil), s.candidates...) }
func (Snapshot) String() string            { return "[redacted routing snapshot]" }
func (Snapshot) GoString() string          { return "[redacted routing snapshot]" }
func (Snapshot) LogValue() slog.Value      { return slog.StringValue("[redacted routing snapshot]") }

type modelFacts struct {
	id, userID, revision, bindingRevision int64
	provider, model, fullName, strategy   string
	silentRetry, flattenToolCalls         int
}

func (s *Store) Snapshot(ctx context.Context, ownerUserID int64, identity Identity) (Snapshot, error) {
	if s == nil || s.db == nil || ctx == nil || ownerUserID <= 0 {
		return Snapshot{}, ErrInvalidIdentity
	}
	modelID, hasID, err := parseModelID(identity.ModelID)
	if err != nil || (identity.FullName != "" && !validFullName(identity.FullName)) || (!hasID && identity.FullName == "") {
		return Snapshot{}, ErrInvalidIdentity
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, fmt.Errorf("routing: begin snapshot: %w", err)
	}
	defer tx.Rollback()
	var facts modelFacts
	if hasID {
		facts, err = readModelByID(ctx, tx, ownerUserID, modelID)
		if err != nil {
			return Snapshot{}, err
		}
	}
	if identity.FullName != "" {
		byName, err := readModelByName(ctx, tx, ownerUserID, identity.FullName)
		if err != nil {
			return Snapshot{}, err
		}
		if hasID && byName.id != facts.id {
			return Snapshot{}, ErrAmbiguousIdentity
		}
		facts = byName
	}
	candidates, err := readCandidates(ctx, tx, ownerUserID, facts.id)
	if err != nil {
		return Snapshot{}, err
	}
	if len(candidates) == 0 {
		return Snapshot{}, ErrUnbound
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("routing: commit snapshot read: %w", err)
	}
	return Snapshot{
		modelID: facts.id, ownerUserID: facts.userID, provider: facts.provider,
		model: facts.model, fullName: facts.fullName, routeStrategy: facts.strategy,
		silentRetry: facts.silentRetry == 1, flattenToolCalls: facts.flattenToolCalls == 1,
		revision: facts.revision, bindingRevision: facts.bindingRevision,
		candidates: append([]Candidate(nil), candidates...),
	}, nil
}

func validFullName(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	count := utf8.RuneCountInString(value)
	if count < 3 || count > 129 {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func parseModelID(value string) (int64, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, false, ErrInvalidIdentity
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, false, ErrInvalidIdentity
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, false, ErrInvalidIdentity
	}
	return id, true, nil
}

func readModelByID(ctx context.Context, tx *sql.Tx, ownerUserID, modelID int64) (modelFacts, error) {
	return scanModelFacts(tx.QueryRowContext(ctx, `
SELECT id,user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision
FROM models WHERE id=? AND user_id=?`, modelID, ownerUserID))
}

func readModelByName(ctx context.Context, tx *sql.Tx, ownerUserID int64, fullName string) (modelFacts, error) {
	return scanModelFacts(tx.QueryRowContext(ctx, `
SELECT id,user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision
FROM models WHERE full_name=? AND user_id=?`, fullName, ownerUserID))
}

func scanModelFacts(row *sql.Row) (modelFacts, error) {
	var facts modelFacts
	err := row.Scan(&facts.id, &facts.userID, &facts.provider, &facts.model, &facts.fullName,
		&facts.strategy, &facts.silentRetry, &facts.flattenToolCalls, &facts.revision, &facts.bindingRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return modelFacts{}, ErrNotFound
	}
	if err != nil {
		return modelFacts{}, fmt.Errorf("routing: read model: %w", err)
	}
	if facts.id <= 0 || facts.userID <= 0 || facts.revision < 1 || facts.bindingRevision < 0 ||
		(facts.strategy != "ordered" && facts.strategy != "random") || facts.fullName != facts.provider+"/"+facts.model ||
		facts.silentRetry < 0 || facts.silentRetry > 1 || facts.flattenToolCalls < 0 || facts.flattenToolCalls > 1 {
		return modelFacts{}, errors.New("routing: invalid persisted model")
	}
	return facts, nil
}

func readCandidates(ctx context.Context, tx *sql.Tx, ownerUserID, modelID int64) ([]Candidate, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT b.id,e.id,k.id,e.connector_type,e.base_url,b.upstream_model_id,k.force_store_false,e.revision,k.revision,b.ord
FROM model_bindings b
JOIN models m ON m.id=b.model_id
JOIN endpoint_keys k ON k.id=b.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
JOIN model_pair_catalog p ON p.endpoint_key_id=k.id AND p.normalized_model_id=b.upstream_model_id
JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
WHERE m.id=? AND m.user_id=? AND e.user_id=? AND e.enabled=1 AND k.enabled=1
  AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
  AND (p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision))
ORDER BY b.ord,b.id`, modelID, ownerUserID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("routing: read candidates: %w", err)
	}
	defer rows.Close()
	var candidates []Candidate
	seen := make(map[string]struct{})
	for rows.Next() {
		var candidate Candidate
		var forceStore int
		if err := rows.Scan(&candidate.bindingID, &candidate.endpointID, &candidate.endpointKeyID,
			&candidate.connectorType, &candidate.canonicalBaseURL, &candidate.upstreamModelID,
			&forceStore, &candidate.endpointRevision, &candidate.keyRevision, &candidate.order); err != nil {
			return nil, fmt.Errorf("routing: scan candidate: %w", err)
		}
		if forceStore < 0 || forceStore > 1 || candidate.bindingID <= 0 || candidate.endpointID <= 0 ||
			candidate.endpointKeyID <= 0 || candidate.endpointRevision < 1 || candidate.keyRevision < 1 ||
			candidate.order < 0 || candidate.order > 255 {
			return nil, errors.New("routing: invalid persisted candidate")
		}
		if forceStore == 1 && candidate.connectorType != string(connectorcontract.TypeOpenAICompatible) {
			return nil, errors.New("routing: incompatible persisted candidate policy")
		}
		identity := strconv.FormatInt(candidate.endpointKeyID, 10) + "\x00" + candidate.upstreamModelID
		if _, duplicate := seen[identity]; duplicate {
			return nil, ErrAmbiguousIdentity
		}
		seen[identity] = struct{}{}
		candidate.forceStoreFalse = forceStore == 1
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("routing: read candidates: %w", err)
	}
	return candidates, nil
}
