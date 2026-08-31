package resources

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	maxUnixSecond        = int64(253402300799)
	defaultPageLimit     = 50
	maxPageLimit         = 100
	cursorLifetime       = int64(24 * 60 * 60)
	maxNoteRunes         = 1024
	maxModelNameRunes    = 64
	maxCatalogPairRows   = 1024
	maxBindingBatch      = 256
	maxDiscoveryRows     = 1000
	staleDiscoverySecond = int64(5 * 60)
)

type Config struct {
	Store           *db.Store
	Connectors      *connector.Registry
	BaseURLs        BaseURLValidator
	Secrets         SecretWriter
	KeyDeletion     EndpointKeyDeletionHook
	KeyCreation     EndpointKeyCreationHook
	Projection      ResourceProjectionHook
	DiscoveryRail   DiscoveryClaimRail
	DiscoveryWorker DiscoveryWorker
	CursorKeys      CursorKeyDeriver
	FinalAuth       FinalTxAuthorizer
	Random          io.Reader
	Now             func() time.Time
	OperationID     func() (string, error)
}

type Repository struct {
	db              *sql.DB
	connectors      *connector.Registry
	baseURLs        BaseURLValidator
	secrets         SecretWriter
	keyDeletion     EndpointKeyDeletionHook
	keyCreation     EndpointKeyCreationHook
	projection      ResourceProjectionHook
	discoveryRail   DiscoveryClaimRail
	discoveryWorker DiscoveryWorker
	finalAuth       FinalTxAuthorizer
	cursors         cursorCodec
	random          io.Reader
	now             func() time.Time
	operationID     func() (string, error)
}

func New(config Config) (*Repository, error) {
	if config.Store == nil || config.Store.DB() == nil || config.Connectors == nil ||
		isNilInterface(config.BaseURLs) || isNilInterface(config.Secrets) ||
		isNilInterface(config.KeyDeletion) || isNilInterface(config.KeyCreation) ||
		isNilInterface(config.Projection) ||
		isNilInterface(config.DiscoveryRail) || isNilInterface(config.DiscoveryWorker) ||
		isNilInterface(config.CursorKeys) ||
		isNilInterface(config.FinalAuth) {
		return nil, errors.New("resources: complete dependencies are required")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OperationID == nil {
		config.OperationID = func() (string, error) { return db.GenerateOpaqueID("op_") }
	}
	return &Repository{
		db: config.Store.DB(), connectors: config.Connectors, baseURLs: config.BaseURLs,
		secrets: config.Secrets, keyDeletion: config.KeyDeletion, keyCreation: config.KeyCreation,
		projection: config.Projection, discoveryRail: config.DiscoveryRail,
		discoveryWorker: config.DiscoveryWorker,
		finalAuth:       config.FinalAuth,
		cursors:         cursorCodec{keys: config.CursorKeys}, random: config.Random,
		now: config.Now, operationID: config.OperationID,
	}, nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (r *Repository) nowUnix() (int64, error) {
	if r == nil || r.now == nil {
		return 0, ErrUnavailable
	}
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond-idempotency.ReplayWindowSeconds {
		return 0, ErrUnavailable
	}
	return now, nil
}

func beginTx(ctx context.Context, database *sql.DB) (*sql.Tx, error) {
	if ctx == nil || database == nil {
		return nil, ErrUnavailable
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("resources: begin transaction: %w", err)
	}
	return tx, nil
}

func (r *Repository) beginAuthorizedTx(ctx context.Context, userID int64) (*sql.Tx, error) {
	if r == nil || userID <= 0 || isNilInterface(r.finalAuth) {
		return nil, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	if err := r.finalAuth.AuthorizeUserMutation(ctx, tx, userID); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("resources: final transaction authorization: %w", err)
	}
	return tx, nil
}

func finishTx(tx *sql.Tx, committed *bool) {
	if tx != nil && committed != nil && !*committed {
		_ = tx.Rollback()
	}
}

func commitTx(tx *sql.Tx, committed *bool) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("resources: commit transaction: %w", err)
	}
	*committed = true
	return nil
}

func actorHash(userID int64) ([32]byte, error) {
	if userID <= 0 {
		return [32]byte{}, ErrNotFound
	}
	value, err := idempotency.ActorScopeHash("user", strconv.FormatInt(userID, 10))
	if err != nil {
		return [32]byte{}, ErrInvalidRequest
	}
	return value, nil
}

func beginControlMutation(ctx context.Context, tx *sql.Tx, userID int64, input ControlMutation, now int64) (idempotency.Decision, error) {
	actor, err := actorHash(userID)
	if err != nil {
		return idempotency.Decision{}, err
	}
	if len(input.CanonicalBody) > idempotency.MaxControlBodyBytes {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: input.Method, Route: input.Route,
		PathResourceIDs: input.PathIDs, Query: input.Query, Body: input.CanonicalBody,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor,
		Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return idempotency.Decision{}, ErrConflict
	}
	if err != nil {
		return idempotency.Decision{}, fmt.Errorf("resources: accept control mutation: %w", err)
	}
	return decision, nil
}

func completeMutation(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, body []byte) error {
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return fmt.Errorf("resources: complete control mutation: %w", err)
	}
	return nil
}

func replayMutation[T any](decision idempotency.Decision) (MutationResult[T], error) {
	var value T
	if len(decision.ResponseBody) > 0 {
		if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
			return MutationResult[T]{}, ErrUnavailable
		}
	}
	return MutationResult[T]{
		Value: value, Status: decision.HTTPStatus,
		Body: append([]byte(nil), decision.ResponseBody...), Replayed: true,
	}, nil
}

func finishJSONMutation[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, value T) (MutationResult[T], error) {
	body, err := json.Marshal(value)
	if err != nil {
		return MutationResult[T]{}, ErrUnavailable
	}
	if len(body) > idempotency.MaxResponseBytes {
		return MutationResult[T]{}, ErrResourceLimit
	}
	if err := completeMutation(ctx, tx, decision, status, body); err != nil {
		return MutationResult[T]{}, err
	}
	return MutationResult[T]{Value: value, Status: status, Body: body}, nil
}

func finishEmptyMutation(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int) (MutationResult[struct{}], error) {
	if err := completeMutation(ctx, tx, decision, status, nil); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return MutationResult[struct{}]{Status: status, Body: []byte{}}, nil
}

func canonicalDecimal(value int64) (string, error) {
	if value < 0 {
		return "", ErrUnavailable
	}
	return strconv.FormatInt(value, 10), nil
}

func decimalID(value int64) (string, error) {
	id, err := db.DecimalID(value)
	if err != nil {
		return "", ErrUnavailable
	}
	return id, nil
}

func parseDecimalID(value string) (int64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, ErrInvalidRequest
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, ErrInvalidRequest
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalidRequest
	}
	return id, nil
}

func mutationPathIDs(input ControlMutation, ids ...int64) bool {
	if len(input.PathIDs) != len(ids) {
		return false
	}
	for index, id := range ids {
		if id <= 0 || input.PathIDs[index] != strconv.FormatInt(id, 10) {
			return false
		}
	}
	return true
}

func validateExactText(value string, minRunes, maxRunes int) bool {
	if !validateFreeText(value, minRunes, maxRunes) {
		return false
	}
	if value != "" {
		first, _ := utf8.DecodeRuneInString(value)
		last, _ := utf8.DecodeLastRuneInString(value)
		if unicode.IsSpace(first) || unicode.IsSpace(last) {
			return false
		}
	}
	return true
}

func validateFreeText(value string, minRunes, maxRunes int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	count := utf8.RuneCountInString(value)
	if count < minRunes || count > maxRunes {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func validateNote(value string) bool {
	return validateFreeText(value, 0, maxNoteRunes)
}

func validPageLimit(limit int) bool {
	return limit >= 1 && limit <= maxPageLimit
}

func normalizePageLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func readSiteLimitTx(ctx context.Context, tx *sql.Tx, key string, minimum, maximum int64) (int64, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&raw); err != nil {
		return 0, fmt.Errorf("resources: read %s: %w", key, err)
	}
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		if raw != "0" {
			return 0, ErrUnavailable
		}
	}
	for index := range raw {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, ErrUnavailable
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < minimum || value > maximum {
		return 0, ErrUnavailable
	}
	return value, nil
}

func countTx(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	var count int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil || count < 0 {
		if err != nil {
			return 0, err
		}
		return 0, ErrUnavailable
	}
	return count, nil
}
