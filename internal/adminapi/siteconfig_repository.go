package adminapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	moderncsqlite "modernc.org/sqlite"
)

const maxSiteConfigUnixSecond = int64(253402300799)

var (
	ErrSiteConfigInvalid      = errors.New("admin site configuration: invalid request")
	ErrSiteConfigUnauthorized = errors.New("admin site configuration: unauthorized")
	ErrSiteConfigForbidden    = errors.New("admin site configuration: forbidden")
	ErrSiteConfigNotFound     = errors.New("admin site configuration: not found")
	ErrSiteConfigConflict     = errors.New("admin site configuration: conflict")
	ErrSiteConfigUnavailable  = errors.New("admin site configuration: unavailable")
	ErrSiteConfigInvariant    = errors.New("admin site configuration: invariant violation")
)

// SiteConfigFinalAuthorizer revalidates the request-bound administrator
// session, credential generation, and live role inside every read or mutation
// transaction. An entry authorization result is never accepted as a ticket.
type SiteConfigFinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}

// SiteConfigRepositoryOptions contains only the authorities owned outside
// this package. The repository never creates schema or caches authorization.
type SiteConfigRepositoryOptions struct {
	Store           *db.Store
	FinalAuthorizer SiteConfigFinalAuthorizer
	Now             func() time.Time
}

type SiteConfigRepository struct {
	database        *sql.DB
	finalAuthorizer SiteConfigFinalAuthorizer
	now             func() time.Time
}

func NewSiteConfigRepository(options SiteConfigRepositoryOptions) (*SiteConfigRepository, error) {
	if options.Store == nil || options.Store.DB() == nil || nilSiteConfigInterface(options.FinalAuthorizer) {
		return nil, errors.New("admin site configuration: store and final authorizer are required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &SiteConfigRepository{database: options.Store.DB(), finalAuthorizer: options.FinalAuthorizer, now: now}, nil
}

type SiteConfigSnapshot struct {
	Revision string         `json:"revision"`
	Values   map[string]any `json:"values"`
}

// SiteConfigCatalogEntry is the frozen catalog DTO. The alias keeps the
// existing catalog builder as the single metadata owner while exposing the
// response type to root composition and contract tests.
type SiteConfigCatalogEntry = siteConfigCatalogEntry

type SiteConfigCatalog struct {
	Data []SiteConfigCatalogEntry `json:"data"`
}

type SiteConfigPatchResponse struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Revision string `json:"revision"`
}

type SiteConfigPatchInput struct {
	AdminID        int64
	Key            string
	RawValue       json.RawMessage
	IdempotencyKey string
}

type SiteConfigMutationResult struct {
	Status   int
	Body     []byte
	Response SiteConfigPatchResponse
	Replayed bool
}

func (repository *SiteConfigRepository) ReadAdminPublicConfig(ctx context.Context, adminID int64) (PublicConfig, error) {
	tx, err := repository.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return PublicConfig{}, err
	}
	defer tx.Rollback()
	stored, err := readKnownSiteConfigRows(ctx, tx)
	if err != nil {
		return PublicConfig{}, err
	}
	value := buildPublicConfig(stored)
	if err := tx.Commit(); err != nil {
		return PublicConfig{}, classifySiteConfigDatabase("commit bootstrap read", err)
	}
	return value, nil
}

func (repository *SiteConfigRepository) ReadSiteConfig(ctx context.Context, adminID int64) (SiteConfigSnapshot, error) {
	tx, err := repository.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return SiteConfigSnapshot{}, err
	}
	defer tx.Rollback()
	revision, err := readSiteConfigRevision(ctx, tx)
	if err != nil {
		return SiteConfigSnapshot{}, err
	}
	stored, err := readKnownSiteConfigRows(ctx, tx)
	if err != nil {
		return SiteConfigSnapshot{}, err
	}
	entries, err := buildSiteConfigCatalog(stored)
	if err != nil {
		return SiteConfigSnapshot{}, fmt.Errorf("%w: build catalog", ErrSiteConfigInvariant)
	}
	values := make(map[string]any, len(entries))
	for _, entry := range entries {
		values[entry.Key] = typedSiteConfigValue(entry.Key, stored[entry.Key])
	}
	if err := tx.Commit(); err != nil {
		return SiteConfigSnapshot{}, classifySiteConfigDatabase("commit configuration read", err)
	}
	return SiteConfigSnapshot{Revision: strconv.FormatInt(revision, 10), Values: values}, nil
}

func (repository *SiteConfigRepository) ReadSiteConfigCatalog(ctx context.Context, adminID int64) (SiteConfigCatalog, error) {
	tx, err := repository.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return SiteConfigCatalog{}, err
	}
	defer tx.Rollback()
	stored, err := readKnownSiteConfigRows(ctx, tx)
	if err != nil {
		return SiteConfigCatalog{}, err
	}
	entries, err := buildSiteConfigCatalog(stored)
	if err != nil {
		return SiteConfigCatalog{}, fmt.Errorf("%w: build catalog", ErrSiteConfigInvariant)
	}
	if err := tx.Commit(); err != nil {
		return SiteConfigCatalog{}, classifySiteConfigDatabase("commit catalog read", err)
	}
	return SiteConfigCatalog{Data: entries}, nil
}

func (repository *SiteConfigRepository) PatchSiteConfig(ctx context.Context, input SiteConfigPatchInput) (SiteConfigMutationResult, error) {
	if repository == nil || ctx == nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	if input.AdminID <= 0 {
		return SiteConfigMutationResult{}, ErrSiteConfigUnauthorized
	}
	tx, err := repository.beginAuthorized(ctx, input.AdminID, false)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	defer tx.Rollback()

	if !knownSiteConfigKey(input.Key) {
		return SiteConfigMutationResult{}, ErrSiteConfigNotFound
	}
	if isSpecializedConfigKey(input.Key) {
		return SiteConfigMutationResult{}, ErrSiteConfigConflict
	}
	patch, err := validateSiteConfigPatch(input.Key, input.RawValue)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	canonicalBody, err := idempotency.CanonicalJSON(struct {
		Value any `json:"value"`
	}{Value: patch.wire})
	if err != nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}
	actorHash, err := idempotency.ActorScopeHash("admin", strconv.FormatInt(input.AdminID, 10))
	if err != nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash:  actorHash,
		Method:          http.MethodPatch,
		Route:           RouteAdminSiteConfigKey,
		PathResourceIDs: []string{input.Key},
		Query:           "",
		Body:            canonicalBody,
	})
	if err != nil {
		return SiteConfigMutationResult{}, ErrSiteConfigInvalid
	}

	// Establish the configuration revision in this transaction before the
	// replay reservation takes the SQLite writer. Overlapping mutations that
	// observed the same revision therefore have one winner; a stale snapshot
	// cannot silently serialize into a second update.
	revision, err := readSiteConfigRevision(ctx, tx)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	decisionNow := repository.now().UTC().Unix()
	if decisionNow < 0 || decisionNow > maxSiteConfigUnixSecond-idempotency.ReplayWindowSeconds {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope:       idempotency.ScopeControlMutation,
		ActorHash:   actorHash,
		Key:         input.IdempotencyKey,
		RequestHash: requestHash,
		DecisionNow: decisionNow,
	})
	if err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigIdempotency(err)
	}
	if decision.Kind == idempotency.Replay {
		result, err := replaySiteConfigMutation(decision, input.Key)
		if err != nil {
			return SiteConfigMutationResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return SiteConfigMutationResult{}, classifySiteConfigDatabase("commit configuration replay", err)
		}
		return result, nil
	}

	stored, err := readKnownSiteConfigRows(ctx, tx)
	if err != nil {
		return SiteConfigMutationResult{}, err
	}
	if patch.remove {
		delete(stored, input.Key)
	} else {
		stored[input.Key] = patch.stored
	}
	if err := db.ValidateGenerationTwoConfigSnapshot(stored); err != nil {
		return SiteConfigMutationResult{}, siteConfigCombinationError(err)
	}
	if input.Key == KeySiteTimezoneOffsetMinutes {
		if err := ensureSiteTimezoneMutable(ctx, tx); err != nil {
			return SiteConfigMutationResult{}, err
		}
	}
	if revision == math.MaxInt64 {
		return SiteConfigMutationResult{}, ErrSiteConfigConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE config_revisions
		SET revision=revision+1,updated_at=?
		WHERE domain='site' AND revision=? AND revision<9223372036854775807`, decisionNow, revision)
	if err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigDatabase("advance configuration revision", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigDatabase("observe configuration revision", err)
	}
	if changed != 1 {
		return SiteConfigMutationResult{}, ErrSiteConfigConflict
	}
	if patch.remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM site_config WHERE key=?`, input.Key); err != nil {
			return SiteConfigMutationResult{}, classifySiteConfigDatabase("delete optional configuration", err)
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, input.Key, patch.stored, decisionNow); err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigDatabase("write configuration", err)
	}

	response := SiteConfigPatchResponse{Key: input.Key, Value: patch.wire, Revision: strconv.FormatInt(revision+1, 10)}
	body, err := json.Marshal(response)
	if err != nil || len(body) > idempotency.MaxResponseBytes {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusOK, body); err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigIdempotencyComplete(err)
	}
	if err := tx.Commit(); err != nil {
		return SiteConfigMutationResult{}, classifySiteConfigDatabase("commit configuration mutation", err)
	}
	return SiteConfigMutationResult{Status: http.StatusOK, Body: body, Response: response}, nil
}

type validatedSiteConfigPatch struct {
	stored string
	wire   any
	remove bool
}

func validateSiteConfigPatch(key string, raw json.RawMessage) (validatedSiteConfigPatch, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return validatedSiteConfigPatch{}, ErrSiteConfigInvalid
	}
	if bytes.Equal(trimmed, []byte("null")) {
		if key != KeyAnthropicDefaultMaxTokens {
			return validatedSiteConfigPatch{}, ErrSiteConfigInvalid
		}
		return validatedSiteConfigPatch{wire: nil, remove: true}, nil
	}
	stored, validation := validateSiteConfigValue(key, raw)
	if validation.Code != "" {
		if validation.Code == "not_found" {
			return validatedSiteConfigPatch{}, ErrSiteConfigNotFound
		}
		if validation.Code == "conflict" {
			return validatedSiteConfigPatch{}, ErrSiteConfigConflict
		}
		return validatedSiteConfigPatch{}, ErrSiteConfigInvalid
	}
	return validatedSiteConfigPatch{stored: stored, wire: typedSiteConfigValue(key, stored)}, nil
}

func (repository *SiteConfigRepository) beginAuthorized(ctx context.Context, adminID int64, readOnly bool) (*sql.Tx, error) {
	if repository == nil || repository.database == nil || repository.finalAuthorizer == nil || ctx == nil {
		return nil, ErrSiteConfigUnavailable
	}
	if adminID <= 0 {
		return nil, ErrSiteConfigUnauthorized
	}
	options := &sql.TxOptions{}
	if readOnly {
		options.ReadOnly = true
	}
	tx, err := repository.database.BeginTx(ctx, options)
	if err != nil {
		return nil, classifySiteConfigDatabase("begin authorized transaction", err)
	}
	if err := classifySiteConfigAuthorization(repository.finalAuthorizer.AuthorizeAdmin(ctx, tx, adminID)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func readKnownSiteConfigRows(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT key,value FROM site_config ORDER BY key`)
	if err != nil {
		return nil, classifySiteConfigDatabase("read configuration rows", err)
	}
	defer rows.Close()
	stored := make(map[string]string, len(knownSiteConfig))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, classifySiteConfigDatabase("scan configuration row", err)
		}
		if knownSiteConfigKey(key) {
			stored[key] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, classifySiteConfigDatabase("iterate configuration rows", err)
	}
	return stored, nil
}

func readSiteConfigRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&revision); err != nil {
		return 0, classifySiteConfigDatabase("read configuration revision", err)
	}
	if revision < 1 {
		return 0, ErrSiteConfigInvariant
	}
	return revision, nil
}

func ensureSiteTimezoneMutable(ctx context.Context, tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM site_config WHERE key='site_timezone_offset_locked'
	)`).Scan(&exists); err != nil {
		return classifySiteConfigDatabase("read timezone lock", err)
	}
	if exists != 0 {
		return ErrSiteConfigConflict
	}
	for _, table := range []string{"checkins", "user_activity_daily", "site_activity_daily"} {
		// The identifiers come only from this fixed internal list.
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` LIMIT 1)`).Scan(&exists); err != nil {
			return classifySiteConfigDatabase("read timezone data guard", err)
		}
		if exists != 0 {
			return ErrSiteConfigConflict
		}
	}
	return nil
}

func replaySiteConfigMutation(decision idempotency.Decision, key string) (SiteConfigMutationResult, error) {
	if decision.HTTPStatus != http.StatusOK || len(decision.ResponseBody) == 0 {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	var response SiteConfigPatchResponse
	if err := json.Unmarshal(decision.ResponseBody, &response); err != nil || response.Key != key || response.Revision == "" {
		return SiteConfigMutationResult{}, ErrSiteConfigInvariant
	}
	return SiteConfigMutationResult{
		Status: decision.HTTPStatus, Body: append([]byte(nil), decision.ResponseBody...),
		Response: response, Replayed: true,
	}, nil
}

func classifySiteConfigAuthorization(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authz.ErrUnauthorized), errors.Is(err, ErrSiteConfigUnauthorized):
		return ErrSiteConfigUnauthorized
	case errors.Is(err, authz.ErrForbidden), errors.Is(err, authz.ErrElevatedRequired), errors.Is(err, ErrSiteConfigForbidden):
		return ErrSiteConfigForbidden
	case errors.Is(err, authz.ErrNotFound), errors.Is(err, ErrSiteConfigNotFound):
		return ErrSiteConfigNotFound
	default:
		return fmt.Errorf("%w: final authorization", ErrSiteConfigInvariant)
	}
}

func classifySiteConfigIdempotency(err error) error {
	switch {
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return ErrSiteConfigConflict
	case errors.Is(err, idempotency.ErrState):
		return ErrSiteConfigInvariant
	case sqliteBusy(err):
		// The revision was read before Begin. A failed write upgrade therefore
		// means an overlapping mutation won the observed revision.
		return ErrSiteConfigConflict
	default:
		return classifySiteConfigDatabase("accept control mutation", err)
	}
}

func classifySiteConfigIdempotencyComplete(err error) error {
	if errors.Is(err, idempotency.ErrState) {
		return ErrSiteConfigInvariant
	}
	return classifySiteConfigDatabase("complete control mutation", err)
}

func classifySiteConfigDatabase(operation string, err error) error {
	if err == nil {
		return nil
	}
	if sqliteBusy(err) {
		return fmt.Errorf("%w: %s", ErrSiteConfigUnavailable, operation)
	}
	return fmt.Errorf("%w: %s", ErrSiteConfigInvariant, operation)
}

func sqliteBusy(err error) bool {
	var sqliteErr *moderncsqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code() & 0xff
	return code == 5 || code == 6
}
