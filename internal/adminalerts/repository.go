package adminalerts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	defaultPageLimit      = 50
	maxPageLimit          = 100
	maxCursorBytes        = 512
	cursorLifetimeSeconds = int64(24 * 60 * 60)
	maxUnixSecond         = int64(253402300799)
	maxAlertMessageRunes  = 1024
	maxAlertRefRunes      = 256
	alertCursorScope      = "admin-alerts:list"
)

type Config struct {
	Store      *db.Store
	CursorKeys CursorKeyDeriver
	FinalAuth  AdminFinalAuthorizer
	Now        func() time.Time
}

type Repository struct {
	database   *sql.DB
	cursorKeys CursorKeyDeriver
	finalAuth  AdminFinalAuthorizer
	now        func() time.Time
}

func NewRepository(config Config) (*Repository, error) {
	if config.Store == nil || config.Store.DB() == nil || isNilInterface(config.CursorKeys) || isNilInterface(config.FinalAuth) {
		return nil, errors.New("administrator alerts: complete dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Repository{
		database: config.Store.DB(), cursorKeys: config.CursorKeys,
		finalAuth: config.FinalAuth, now: config.Now,
	}, nil
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

func validLimit(limit int) bool {
	return limit >= 0 && limit <= maxPageLimit
}

func normalizedLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func (repository *Repository) nowUnix() (int64, error) {
	if repository == nil || repository.now == nil {
		return 0, errors.New("administrator alerts: clock unavailable")
	}
	now := repository.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return 0, errors.New("administrator alerts: invalid clock")
	}
	return now, nil
}

func (repository *Repository) beginAuthorized(ctx context.Context, adminID int64, readOnly bool) (*sql.Tx, error) {
	if adminID <= 0 {
		return nil, ErrUnauthorized
	}
	if repository == nil || repository.database == nil || isNilInterface(repository.finalAuth) || ctx == nil {
		return nil, errors.New("administrator alerts: repository unavailable")
	}
	options := &sql.TxOptions{ReadOnly: readOnly}
	tx, err := repository.database.BeginTx(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("administrator alerts: begin transaction: %w", err)
	}
	if err := classifyAuthorizationError(repository.finalAuth.AuthorizeAdmin(ctx, tx, adminID)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func finishTransaction(tx *sql.Tx, committed *bool) {
	if tx != nil && committed != nil && !*committed {
		_ = tx.Rollback()
	}
}

func commitTransaction(tx *sql.Tx, committed *bool) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("administrator alerts: commit transaction: %w", err)
	}
	*committed = true
	return nil
}

func alertCursorOwner(resolved *bool) string {
	if resolved == nil {
		return "all"
	}
	if *resolved {
		return "resolved:true"
	}
	return "resolved:false"
}

func (repository *Repository) deriveCursorKey() ([]byte, error) {
	if repository == nil || isNilInterface(repository.cursorKeys) {
		return nil, errors.New("administrator alerts: cursor key unavailable")
	}
	key, err := repository.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, errors.New("administrator alerts: cursor key unavailable")
	}
	return key, nil
}

func (repository *Repository) decodeCursor(token string, resolved *bool, now int64) (int64, error) {
	if token == "" || len(token) > maxCursorBytes {
		return 0, ErrInvalidRequest
	}
	key, err := repository.deriveCursorKey()
	if err != nil {
		return 0, err
	}
	defer clear(key)
	cursor, err := db.DecodePaginationCursorWithDerivedKey(
		key, token, alertCursorScope, alertCursorOwner(resolved), uint64(now),
	)
	if err != nil || len(cursor.Atoms) != 1 || cursor.Atoms[0].Kind != db.CursorUint ||
		cursor.Atoms[0].Uint == 0 || cursor.Atoms[0].Uint > uint64(math.MaxInt64) {
		return 0, ErrInvalidRequest
	}
	return int64(cursor.Atoms[0].Uint), nil
}

func (repository *Repository) encodeCursor(id int64, resolved *bool, now int64) (string, error) {
	if id <= 0 || now > maxUnixSecond-cursorLifetimeSeconds {
		return "", fmt.Errorf("%w: invalid pagination state", ErrInvariant)
	}
	key, err := repository.deriveCursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	token, err := db.EncodePaginationCursorWithDerivedKey(
		key, alertCursorScope, alertCursorOwner(resolved), uint64(now+cursorLifetimeSeconds),
		[]db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(id)}},
	)
	if err != nil || token == "" || len(token) > maxCursorBytes {
		return "", fmt.Errorf("%w: encode pagination cursor", ErrInvariant)
	}
	return token, nil
}

type rawAlert struct {
	id            int64
	kind          string
	message       string
	ref           string
	subjectUserID sql.NullInt64
	createdAt     int64
	resolved      int
	resolvedAt    sql.NullInt64
}

type rowScanner interface {
	Scan(...any) error
}

func scanRawAlert(scanner rowScanner) (rawAlert, error) {
	var row rawAlert
	err := scanner.Scan(&row.id, &row.kind, &row.message, &row.ref, &row.subjectUserID,
		&row.createdAt, &row.resolved, &row.resolvedAt)
	return row, err
}

func validAlertText(value string, maxRunes int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, codepoint := range value {
		if codepoint <= 0x1f || codepoint == 0x7f || codepoint >= 0x80 && codepoint <= 0x9f {
			return false
		}
	}
	return true
}

func projectAlert(row rawAlert) (AdminAlert, error) {
	if row.id <= 0 || !validKind(row.kind) || !validAlertText(row.message, maxAlertMessageRunes) ||
		!validAlertText(row.ref, maxAlertRefRunes) || row.createdAt < 0 || row.createdAt > maxUnixSecond ||
		(row.resolved != 0 && row.resolved != 1) || row.subjectUserID.Valid && row.subjectUserID.Int64 <= 0 ||
		row.resolvedAt.Valid && (row.resolvedAt.Int64 < 0 || row.resolvedAt.Int64 > maxUnixSecond) ||
		(row.resolved == 1) != row.resolvedAt.Valid {
		return AdminAlert{}, fmt.Errorf("%w: invalid persisted administrator alert", ErrInvariant)
	}
	value := AdminAlert{
		ID: strconv.FormatInt(row.id, 10), Kind: Kind(row.kind), Message: row.message,
		CreatedAt: row.createdAt, Resolved: row.resolved == 1,
	}
	if row.ref != "" {
		ref := row.ref
		value.Ref = &ref
	}
	if row.subjectUserID.Valid {
		subject := strconv.FormatInt(row.subjectUserID.Int64, 10)
		value.SubjectUserID = &subject
	}
	if row.resolvedAt.Valid {
		resolvedAt := row.resolvedAt.Int64
		value.ResolvedAt = &resolvedAt
	}
	return value, nil
}

func (repository *Repository) List(ctx context.Context, adminID int64, query ListQuery) (Page[AdminAlert], error) {
	empty := Page[AdminAlert]{Data: []AdminAlert{}}
	if repository == nil || !validLimit(query.Limit) || len(query.Cursor) > maxCursorBytes {
		return empty, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return empty, err
	}
	tx, err := repository.beginAuthorized(ctx, adminID, true)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTransaction(tx, &committed)

	where := make([]string, 0, 2)
	arguments := make([]any, 0, 4)
	if query.Resolved != nil {
		where = append(where, "resolved=?")
		if *query.Resolved {
			arguments = append(arguments, 1)
		} else {
			arguments = append(arguments, 0)
		}
	}
	if query.Cursor != "" {
		cursorID, err := repository.decodeCursor(query.Cursor, query.Resolved, now)
		if err != nil {
			return empty, err
		}
		where = append(where, "id<?")
		arguments = append(arguments, cursorID)
	}
	statement := `SELECT id,kind,message,ref,subject_user_id,created_at,resolved,resolved_at FROM admin_alerts`
	if len(where) != 0 {
		statement += " WHERE " + strings.Join(where, " AND ")
	}
	limit := normalizedLimit(query.Limit)
	statement += " ORDER BY id DESC LIMIT ?"
	arguments = append(arguments, limit+1)
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return empty, fmt.Errorf("administrator alerts: list rows: %w", err)
	}
	rawRows := make([]rawAlert, 0, limit+1)
	for rows.Next() {
		row, scanErr := scanRawAlert(rows)
		if scanErr != nil {
			_ = rows.Close()
			return empty, fmt.Errorf("administrator alerts: scan row: %w", scanErr)
		}
		rawRows = append(rawRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return empty, fmt.Errorf("administrator alerts: iterate rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return empty, fmt.Errorf("administrator alerts: close rows: %w", err)
	}
	hasMore := len(rawRows) > limit
	if hasMore {
		rawRows = rawRows[:limit]
	}
	page := Page[AdminAlert]{Data: make([]AdminAlert, 0, len(rawRows))}
	for _, row := range rawRows {
		value, err := projectAlert(row)
		if err != nil {
			return empty, err
		}
		page.Data = append(page.Data, value)
	}
	if hasMore && len(rawRows) != 0 {
		cursor, err := repository.encodeCursor(rawRows[len(rawRows)-1].id, query.Resolved, now)
		if err != nil {
			return empty, err
		}
		page.NextCursor = &cursor
	}
	if err := commitTransaction(tx, &committed); err != nil {
		return empty, err
	}
	return page, nil
}

func (repository *Repository) SetResolved(ctx context.Context, adminID, alertID int64, resolved bool) (AdminAlert, error) {
	if repository == nil || alertID <= 0 {
		return AdminAlert{}, ErrInvalidRequest
	}
	now, err := repository.nowUnix()
	if err != nil {
		return AdminAlert{}, err
	}
	tx, err := repository.beginAuthorized(ctx, adminID, false)
	if err != nil {
		return AdminAlert{}, err
	}
	committed := false
	defer finishTransaction(tx, &committed)
	if resolved {
		_, err = tx.ExecContext(ctx, `UPDATE admin_alerts SET resolved=1,resolved_at=? WHERE id=? AND resolved=0`, now, alertID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE admin_alerts SET resolved=0,resolved_at=NULL WHERE id=? AND resolved=1`, alertID)
	}
	if err != nil {
		return AdminAlert{}, fmt.Errorf("administrator alerts: set resolved state: %w", err)
	}
	row, err := scanRawAlert(tx.QueryRowContext(ctx, `
SELECT id,kind,message,ref,subject_user_id,created_at,resolved,resolved_at
FROM admin_alerts WHERE id=?`, alertID))
	if errors.Is(err, sql.ErrNoRows) {
		return AdminAlert{}, ErrNotFound
	}
	if err != nil {
		return AdminAlert{}, fmt.Errorf("administrator alerts: read resolved state: %w", err)
	}
	value, err := projectAlert(row)
	if err != nil {
		return AdminAlert{}, err
	}
	if err := commitTransaction(tx, &committed); err != nil {
		return AdminAlert{}, err
	}
	return value, nil
}
