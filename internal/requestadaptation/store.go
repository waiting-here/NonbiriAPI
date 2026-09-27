package requestadaptation

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const contextKeyLabel = "request-adaptation-context/v1"

type KeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type Config struct {
	DB         *sql.DB
	Codec      secret.GenerationTwoContextCodec
	KeyDeriver KeyDeriver
}

// Store owns only encrypted adaptation data. Authorization must be completed
// by the resource service in the same transaction before LoadTx or SaveTx.
type Store struct {
	db      *sql.DB
	codec   secret.GenerationTwoContextCodec
	deriver KeyDeriver
}

func New(config Config) (*Store, error) {
	if config.DB == nil || config.Codec == nil || config.KeyDeriver == nil {
		return nil, ErrUnavailable
	}
	return &Store{db: config.DB, codec: config.Codec, deriver: config.KeyDeriver}, nil
}

func (s *Store) contextFor(ref Ref) ([]byte, secret.GenerationTwoEndpointKeyContext, error) {
	if s == nil || !ref.valid() {
		return nil, secret.GenerationTwoEndpointKeyContext{}, ErrInvalid
	}
	key, err := s.deriver.DeriveGenerationTwoSubkey([]byte(contextKeyLabel))
	if err != nil || len(key) != secret.SubkeyBytes {
		clear(key)
		return nil, secret.GenerationTwoEndpointKeyContext{}, ErrUnavailable
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(ref.Scope))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(strconv.FormatInt(ref.ID, 10)))
	contextID := append([]byte(nil), mac.Sum(nil)[:16]...)
	clear(key)
	ctx, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
	if err != nil {
		clear(contextID)
		return nil, secret.GenerationTwoEndpointKeyContext{}, ErrUnavailable
	}
	return contextID, ctx, nil
}

func refColumn(scope Scope) string {
	switch scope {
	case ScopeEndpoint:
		return "endpoint_id"
	case ScopeCharityModel:
		return "model_id"
	case ScopeBinding:
		return "binding_id"
	default:
		return ""
	}
}

func project(d Document, revision int64, sources [5]Scope) Projection {
	list := func(section ListSection, source Scope) ListProjection {
		return ListProjection{Mode: section.Mode, Values: append([]string{}, section.Values...), Source: source}
	}
	headers := MapProjection{Mode: d.FixedHeaders.Mode, Values: make(map[string]ValueProjection, len(d.FixedHeaders.Values)), Source: sources[1]}
	for name := range d.FixedHeaders.Values {
		headers.Values[name] = ValueProjection{HasValue: true, Mask: "••••"}
	}
	body := func(section BodySection, source Scope) MapProjection {
		out := MapProjection{Mode: section.Mode, Values: make(map[string]ValueProjection, len(section.Values)), Source: source}
		for path := range section.Values {
			out.Values[path] = ValueProjection{HasValue: true, Mask: "••••"}
		}
		return out
	}
	return Projection{
		Revision:       strconv.FormatInt(revision, 10),
		ForwardHeaders: list(d.ForwardHeaders, sources[0]), FixedHeaders: headers,
		BodyDefaults: body(d.BodyDefaults, sources[2]), BodyForced: body(d.BodyForced, sources[3]),
		NativeExtensionPaths: list(d.NativeExtensionPaths, sources[4]),
	}
}

func (s Snapshot) Projection() Projection {
	revision, _ := strconv.ParseInt(s.Revision, 10, 64)
	out := project(s.Document, revision, s.Sources)
	out.Revision = s.Revision
	return out
}

func (s *Store) LoadTx(ctx context.Context, tx *sql.Tx, ref Ref) (Snapshot, error) {
	if s == nil || tx == nil || !ref.valid() {
		return Snapshot{}, ErrInvalid
	}
	query := "SELECT revision,secret_context,secret_ciphertext FROM request_adaptations WHERE " + refColumn(ref.Scope) + "=? AND scope=?"
	var revision int64
	var contextID []byte
	var ciphertext string
	err := tx.QueryRowContext(ctx, query, ref.ID, ref.Scope).Scan(&revision, &contextID, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{Document: Empty(ref.Scope), Revision: "0"}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("request adaptation: load: %w", err)
	}
	expected, secretCtx, err := s.contextFor(ref)
	if err != nil {
		return Snapshot{}, err
	}
	defer clear(expected)
	if !hmac.Equal(contextID, expected) {
		return Snapshot{}, ErrUnavailable
	}
	plaintext, err := s.codec.OpenForGenerationTwoContext(ciphertext, secretCtx)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer clear(plaintext)
	var doc Document
	if !validJSON(plaintext) || json.Unmarshal(plaintext, &doc) != nil || ValidateDocument(doc, ref.Scope) != nil {
		doc.Clear()
		return Snapshot{}, ErrUnavailable
	}
	return Snapshot{Document: doc, Revision: strconv.FormatInt(revision, 10)}, nil
}

func (s *Store) Load(ctx context.Context, ref Ref) (Snapshot, error) {
	if s == nil || !ref.valid() {
		return Snapshot{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer tx.Rollback()
	snapshot, err := s.LoadTx(ctx, tx, ref)
	if err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		snapshot.Clear()
		return Snapshot{}, ErrUnavailable
	}
	return snapshot, nil
}

// LoadMany reads all candidate settings at one SQLite snapshot. It is called
// once per accepted logical request, before physical attempts begin.
func (s *Store) LoadMany(ctx context.Context, refs []Ref) (map[Ref]Snapshot, error) {
	if s == nil || len(refs) > 202 {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, ErrUnavailable
	}
	defer tx.Rollback()
	out := make(map[Ref]Snapshot, len(refs))
	for _, ref := range refs {
		if !ref.valid() {
			return nil, ErrInvalid
		}
		if _, exists := out[ref]; exists {
			continue
		}
		value, err := s.LoadTx(ctx, tx, ref)
		if err != nil {
			for _, item := range out {
				item.Clear()
			}
			return nil, err
		}
		out[ref] = value
	}
	if err := tx.Commit(); err != nil {
		for _, item := range out {
			item.Clear()
		}
		return nil, ErrUnavailable
	}
	return out, nil
}

// SaveTx performs an optimistic, monotonic revision write. The caller owns
// transaction authorization, resource existence, idempotency, and commit.
func (s *Store) SaveTx(ctx context.Context, tx *sql.Tx, ref Ref, expectedRevision int64, doc Document, actorID, now int64) (Projection, error) {
	if s == nil || tx == nil || !ref.valid() || expectedRevision < 0 || expectedRevision == int64(^uint64(0)>>1) || actorID <= 0 || now < 0 || now > 253402300799 || ValidateDocument(doc, ref.Scope) != nil {
		return Projection{}, ErrInvalid
	}
	previous, err := s.LoadTx(ctx, tx, ref)
	if err != nil {
		return Projection{}, err
	}
	defer previous.Clear()
	if previous.Revision != strconv.FormatInt(expectedRevision, 10) {
		return Projection{}, ErrConflict
	}
	changed, err := json.Marshal(changedPartitions(previous.Document, doc))
	if err != nil {
		return Projection{}, ErrInvalid
	}
	plaintext, err := json.Marshal(doc)
	if err != nil || len(plaintext) > MaxConfigurationBytes || !validJSON(plaintext) {
		clear(plaintext)
		return Projection{}, ErrInvalid
	}
	defer clear(plaintext)
	contextID, secretCtx, err := s.contextFor(ref)
	if err != nil {
		return Projection{}, err
	}
	defer clear(contextID)
	ciphertext, err := s.codec.SealForGenerationTwoContext(plaintext, secretCtx)
	if err != nil {
		return Projection{}, ErrUnavailable
	}
	revision := expectedRevision + 1
	projection := project(doc, revision, [5]Scope{})
	structure, err := json.Marshal(projection)
	if err != nil || len(structure) > MaxConfigurationBytes {
		return Projection{}, ErrInvalid
	}
	column := refColumn(ref.Scope)
	if expectedRevision == 0 {
		query := "INSERT INTO request_adaptations(scope," + column + ",revision,secret_context,secret_ciphertext,structure_json,actor_user_id,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING"
		result, err := tx.ExecContext(ctx, query, ref.Scope, ref.ID, revision, contextID, ciphertext, string(structure), actorID, now)
		if err != nil {
			return Projection{}, fmt.Errorf("request adaptation: insert: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return Projection{}, ErrUnavailable
		}
		if changed != 1 {
			return Projection{}, ErrConflict
		}
	} else {
		query := "UPDATE request_adaptations SET revision=?,secret_ciphertext=?,structure_json=?,actor_user_id=?,updated_at=? WHERE " + column + "=? AND scope=? AND revision=?"
		result, err := tx.ExecContext(ctx, query, revision, ciphertext, string(structure), actorID, now, ref.ID, ref.Scope, expectedRevision)
		if err != nil {
			return Projection{}, fmt.Errorf("request adaptation: update: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return Projection{}, ErrUnavailable
		}
		if updated != 1 {
			return Projection{}, ErrConflict
		}
	}
	role := "admin"
	if ref.Scope == ScopeEndpoint {
		role = "owner"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO request_adaptation_audits(
scope,resource_id,actor_user_id,actor_role,revision,changed_partitions,created_at)
VALUES(?,?,?,?,?,?,?)`, ref.Scope, ref.ID, actorID, role, revision, string(changed), now); err != nil {
		return Projection{}, fmt.Errorf("request adaptation: audit: %w", err)
	}
	return projection, nil
}

func changedPartitions(before, after Document) []string {
	changed := make([]string, 0, 5)
	if !reflect.DeepEqual(before.ForwardHeaders, after.ForwardHeaders) {
		changed = append(changed, "forward_headers")
	}
	if !reflect.DeepEqual(before.FixedHeaders, after.FixedHeaders) {
		changed = append(changed, "fixed_headers")
	}
	if !reflect.DeepEqual(before.BodyDefaults, after.BodyDefaults) {
		changed = append(changed, "body_defaults")
	}
	if !reflect.DeepEqual(before.BodyForced, after.BodyForced) {
		changed = append(changed, "body_forced")
	}
	if !reflect.DeepEqual(before.NativeExtensionPaths, after.NativeExtensionPaths) {
		changed = append(changed, "native_extension_paths")
	}
	return changed
}

// Effective uses complete-partition replacement; charity bindings never
// inherit the donor's endpoint rules.
func Effective(model, binding Snapshot) Snapshot {
	out := Snapshot{Document: cloneDocument(model.Document), Revision: model.Revision + "/" + binding.Revision}
	for i := range out.Sources {
		out.Sources[i] = ScopeCharityModel
	}
	if binding.Document.ForwardHeaders.Mode == ModeReplace {
		out.Document.ForwardHeaders = ListSection{Mode: ModeReplace, Values: append([]string{}, binding.Document.ForwardHeaders.Values...)}
		out.Sources[0] = ScopeBinding
	}
	if binding.Document.FixedHeaders.Mode == ModeReplace {
		out.Document.FixedHeaders = HeaderSection{Mode: ModeReplace, Values: cloneHeaders(binding.Document.FixedHeaders.Values)}
		out.Sources[1] = ScopeBinding
	}
	if binding.Document.BodyDefaults.Mode == ModeReplace {
		out.Document.BodyDefaults = BodySection{Mode: ModeReplace, Values: cloneBody(binding.Document.BodyDefaults.Values)}
		out.Sources[2] = ScopeBinding
	}
	if binding.Document.BodyForced.Mode == ModeReplace {
		out.Document.BodyForced = BodySection{Mode: ModeReplace, Values: cloneBody(binding.Document.BodyForced.Values)}
		out.Sources[3] = ScopeBinding
	}
	if binding.Document.NativeExtensionPaths.Mode == ModeReplace {
		out.Document.NativeExtensionPaths = ListSection{Mode: ModeReplace, Values: append([]string{}, binding.Document.NativeExtensionPaths.Values...)}
		out.Sources[4] = ScopeBinding
	}
	return out
}

func cloneHeaders(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
