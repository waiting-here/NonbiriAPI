package announcements

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	maxUnixSecond             = int64(253402300799)
	maxAnnouncements          = int64(500)
	defaultPageLimit          = 50
	maxPageLimit              = 100
	announcementAuditActorTTL = int64(90 * 24 * 60 * 60)
	announcementAuditTTL      = int64(365 * 24 * 60 * 60)
	maxReasonRunes            = 1024
)

const (
	routeUserAnnouncements  = "/api/announcements"
	routeUserAnnouncement   = "/api/announcements/{id}"
	routeAdminAnnouncements = "/admin/api/announcements"
	routeAdminAnnouncement  = "/admin/api/announcements/{id}"
	routeAdminPreview       = "/admin/api/announcements/{id}/preview"
	routeAdminPublish       = "/admin/api/announcements/{id}/publish"
	routeAdminWithdraw      = "/admin/api/announcements/{id}/withdraw"
)

type Config struct {
	Store          *db.Store
	CursorKeys     CursorKeyDeriver
	FinalAuth      AdminFinalTxAuthorizer
	Now            func() time.Time
	AnnouncementID func() (string, error)
}

type Repository struct {
	db        *sql.DB
	cursors   cursorCodec
	finalAuth AdminFinalTxAuthorizer
	now       func() time.Time
	newID     func() (string, error)
	renderer  markdownRenderer
}

type Service struct {
	repository *Repository
}

func NewRepository(config Config) (*Repository, error) {
	if config.Store == nil || config.Store.DB() == nil || isNilInterface(config.CursorKeys) || isNilInterface(config.FinalAuth) {
		return nil, errors.New("announcements: complete dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.AnnouncementID == nil {
		config.AnnouncementID = func() (string, error) { return db.GenerateOpaqueID("ann_") }
	}
	return &Repository{
		db: config.Store.DB(), cursors: cursorCodec{keys: config.CursorKeys}, finalAuth: config.FinalAuth,
		now: config.Now, newID: config.AnnouncementID,
	}, nil
}

func NewService(repository *Repository) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("announcements: repository is required")
	}
	return &Service{repository: repository}, nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (repository *Repository) nowUnix() (int64, error) {
	if repository == nil || repository.now == nil {
		return 0, ErrUnavailable
	}
	now := repository.now().Unix()
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
		return nil, fmt.Errorf("announcements: begin transaction: %w", err)
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
		return fmt.Errorf("announcements: commit transaction: %w", err)
	}
	*committed = true
	return nil
}

func (repository *Repository) beginAuthorizedAdminTx(ctx context.Context, adminID int64) (*sql.Tx, int64, error) {
	if repository == nil || adminID <= 0 || isNilInterface(repository.finalAuth) {
		return nil, 0, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return nil, 0, err
	}
	tx, err := beginTx(ctx, repository.db)
	if err != nil {
		return nil, 0, err
	}
	if err := repository.finalAuth.AuthorizeAdminFinalTx(ctx, tx, adminID); err != nil {
		_ = tx.Rollback()
		return nil, 0, fmt.Errorf("announcements: final administrator authorization: %w", err)
	}
	return tx, now, nil
}

func beginAnnouncementMutation(ctx context.Context, tx *sql.Tx, adminID int64, mutation ControlMutation, now int64) (idempotency.Decision, error) {
	canonicalAdmin := strconv.FormatInt(adminID, 10)
	actor, err := idempotency.ActorScopeHash("admin", canonicalAdmin)
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: mutation.Method, Route: mutation.Route,
		PathResourceIDs: mutation.PathIDs, Query: mutation.Query, Body: mutation.CanonicalBody,
	})
	if err != nil {
		return idempotency.Decision{}, ErrInvalidRequest
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeAnnouncement, ActorHash: actor, Key: mutation.IdempotencyKey,
		RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return idempotency.Decision{}, ErrConflict
	}
	if err != nil {
		return idempotency.Decision{}, fmt.Errorf("announcements: accept idempotent mutation: %w", err)
	}
	return decision, nil
}

func replayMutation[T any](decision idempotency.Decision) (MutationResult[T], error) {
	var value T
	if len(decision.ResponseBody) > 0 {
		if err := json.Unmarshal(decision.ResponseBody, &value); err != nil {
			return MutationResult[T]{}, ErrUnavailable
		}
	}
	return MutationResult[T]{
		Value: value, Status: decision.HTTPStatus, Body: append([]byte(nil), decision.ResponseBody...), Replayed: true,
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
	if err := idempotency.Complete(ctx, tx, decision, status, body); err != nil {
		return MutationResult[T]{}, fmt.Errorf("announcements: complete idempotent mutation: %w", err)
	}
	return MutationResult[T]{Value: value, Status: status, Body: body}, nil
}

func finishEmptyMutation(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int) (MutationResult[struct{}], error) {
	if err := idempotency.Complete(ctx, tx, decision, status, nil); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("announcements: complete idempotent mutation: %w", err)
	}
	return MutationResult[struct{}]{Status: status, Body: []byte{}}, nil
}

type rawAnnouncement struct {
	id, state, draftTitleZH, draftBodyZH, draftTitleEN, draftBodyEN      string
	publishedTitleZH, publishedBodyZH, publishedTitleEN, publishedBodyEN sql.NullString
	severity                                                             string
	revision, pinned, dismissible, createdAt, updatedAt                  int64
	expiresAt, publishedRevision, publishedAt, withdrawnAt               sql.NullInt64
}

type rowScanner interface {
	Scan(...any) error
}

func scanAnnouncement(scanner rowScanner) (rawAnnouncement, error) {
	var row rawAnnouncement
	err := scanner.Scan(
		&row.id, &row.state, &row.revision,
		&row.draftTitleZH, &row.draftBodyZH, &row.draftTitleEN, &row.draftBodyEN,
		&row.publishedTitleZH, &row.publishedBodyZH, &row.publishedTitleEN, &row.publishedBodyEN,
		&row.severity, &row.pinned, &row.dismissible, &row.expiresAt,
		&row.publishedRevision, &row.publishedAt, &row.withdrawnAt, &row.createdAt, &row.updatedAt,
	)
	if err != nil {
		return rawAnnouncement{}, err
	}
	if !row.valid() {
		return rawAnnouncement{}, ErrUnavailable
	}
	return row, nil
}

const announcementSelectColumns = `
id,state,revision,draft_title_zh,draft_body_zh,draft_title_en,draft_body_en,
published_title_zh,published_body_zh,published_title_en,published_body_en,
severity,pinned,dismissible,expires_at,published_revision,published_at,withdrawn_at,created_at,updated_at`

func (row rawAnnouncement) valid() bool {
	if !dbAnnouncementID(row.id) || row.revision < 1 || row.pinned < 0 || row.pinned > 1 || row.dismissible < 0 || row.dismissible > 1 ||
		!validSeverity(row.severity) || row.createdAt < 0 || row.createdAt > maxUnixSecond || row.updatedAt < row.createdAt || row.updatedAt > maxUnixSecond {
		return false
	}
	switch row.state {
	case "draft", "published", "withdrawn", "expired":
	default:
		return false
	}
	if row.expiresAt.Valid && (row.expiresAt.Int64 < 0 || row.expiresAt.Int64 > maxUnixSecond) {
		return false
	}
	projection := row.publishedRevision.Valid
	if projection != row.publishedAt.Valid || projection != row.publishedTitleZH.Valid || projection != row.publishedBodyZH.Valid ||
		projection != row.publishedTitleEN.Valid || projection != row.publishedBodyEN.Valid {
		return false
	}
	if row.state == "published" && (!projection || row.publishedRevision.Int64 < 1 || row.publishedRevision.Int64 > row.revision) {
		return false
	}
	return true
}

func dbAnnouncementID(value string) bool { return db.ValidateOpaqueID(value, "ann_") }

type draftValues struct {
	titleZH, bodyZH, titleEN, bodyEN string
	severity                         string
	pinned, dismissible              bool
	expiresAt                        *int64
}

func draftFromRow(row rawAnnouncement) draftValues {
	return draftValues{
		titleZH: row.draftTitleZH, bodyZH: row.draftBodyZH, titleEN: row.draftTitleEN, bodyEN: row.draftBodyEN,
		severity: row.severity, pinned: row.pinned == 1, dismissible: row.dismissible == 1,
		expiresAt: nullableInt64(row.expiresAt),
	}
}

func applyDraftPatch(values draftValues, patch DraftPatch) draftValues {
	if patch.TitleZH != nil {
		values.titleZH = *patch.TitleZH
	}
	if patch.BodyZH != nil {
		values.bodyZH = *patch.BodyZH
	}
	if patch.TitleEN != nil {
		values.titleEN = *patch.TitleEN
	}
	if patch.BodyEN != nil {
		values.bodyEN = *patch.BodyEN
	}
	if patch.Severity != nil {
		values.severity = *patch.Severity
	}
	if patch.Pinned != nil {
		values.pinned = *patch.Pinned
	}
	if patch.Dismissible != nil {
		values.dismissible = *patch.Dismissible
	}
	if patch.ExpiresAt.Set {
		values.expiresAt = cloneInt64(patch.ExpiresAt.Value)
	}
	return values
}

func (repository *Repository) validateDraft(values draftValues, now int64) error {
	if !validSeverity(values.severity) || (values.expiresAt != nil && (*values.expiresAt <= now || *values.expiresAt > maxUnixSecond)) {
		return ErrInvalidRequest
	}
	for _, body := range []string{values.bodyZH, values.bodyEN} {
		if len(body) > maxAnnouncementBodyBytes {
			return ErrPayloadTooLarge
		}
	}
	for _, language := range []struct{ title, body string }{{values.titleZH, values.bodyZH}, {values.titleEN, values.bodyEN}} {
		if language.title == "" && language.body == "" {
			continue
		}
		if !validTitle(language.title) || language.body == "" {
			return ErrInvalidRequest
		}
		rendered, err := repository.renderer.render(language.body)
		if err != nil || rendered.plain == "" {
			return ErrInvalidRequest
		}
	}
	return nil
}

func completeLanguage(title, body string) bool { return title != "" && body != "" }

func validSeverity(value string) bool {
	return value == "info" || value == "warning" || value == "important"
}

func validReason(value string, required bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxReasonRunes || len(value) > 4096 || (required && value == "") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func validLimit(limit int) bool { return limit == 0 || (limit >= 1 && limit <= maxPageLimit) }

func normalizedLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return cloneInt64(&value.Int64)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func decimalRevision(value int64) (string, error) {
	if value < 1 {
		return "", ErrUnavailable
	}
	return strconv.FormatInt(value, 10), nil
}

func nextRevision(value int64) (int64, error) {
	if value < 1 || value == int64(^uint64(0)>>1) {
		return 0, ErrUnavailable
	}
	return value + 1, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
