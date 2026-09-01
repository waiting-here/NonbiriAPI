package logapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	cursorLifetime = 24 * time.Hour
	maxUnixSecond  = int64(253402300799)
	defaultLimit   = 50
	maximumLimit   = 100
	maxExportRows  = 10_000
)

type CursorKeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type Repository struct {
	db         *sql.DB
	cursorKeys CursorKeyDeriver
	heldRead   AdminHeldReadAuthorizer
	now        func() time.Time
}

func (repository *Repository) decisionNow() (int64, error) {
	if repository == nil || repository.now == nil {
		return 0, ErrUnavailable
	}
	now := repository.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return 0, ErrUnavailable
	}
	return now, nil
}

func (repository *Repository) beginStewardRead(
	ctx context.Context,
	stewardUserID int64,
	authorizer StewardAuthorizer,
) (*sql.Tx, error) {
	if repository == nil || repository.db == nil || ctx == nil || stewardUserID <= 0 || authorizer == nil {
		return nil, ErrInvalid
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, translateSQLError(err)
	}
	if err := authorizer.AuthorizeStewardRead(ctx, tx, stewardUserID); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, ErrUnavailable) {
			return nil, ErrUnavailable
		}
		return nil, ErrForbidden
	}
	return tx, nil
}

func (repository *Repository) authorizeStewardRead(
	ctx context.Context,
	stewardUserID int64,
	authorizer StewardAuthorizer,
) error {
	tx, err := repository.beginStewardRead(ctx, stewardUserID, authorizer)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Commit(); err != nil {
		return translateSQLError(err)
	}
	return nil
}

func NewRepository(database *sql.DB, cursorKeys CursorKeyDeriver) (*Repository, error) {
	if database == nil || cursorKeys == nil {
		return nil, ErrInvalid
	}
	return &Repository{db: database, cursorKeys: cursorKeys, now: time.Now}, nil
}

func newRepository(database *sql.DB, cursorKeys CursorKeyDeriver, now func() time.Time) (*Repository, error) {
	if database == nil || cursorKeys == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Repository{db: database, cursorKeys: cursorKeys, now: now}, nil
}

type listCursor struct {
	startedAt int64
	rowID     int64
}

func normalizeListFilter(filter ListFilter, role string) (ListFilter, error) {
	if filter.Limit == 0 {
		filter.Limit = defaultLimit
	}
	if filter.Limit < 1 || filter.Limit > maximumLimit || len(filter.Cursor) > 512 {
		return ListFilter{}, ErrInvalid
	}
	if filter.Status != nil && (*filter.Status < 100 || *filter.Status > 599) {
		return ListFilter{}, ErrInvalid
	}
	if filter.From != nil && (*filter.From < 0 || *filter.From > maxUnixSecond) {
		return ListFilter{}, ErrInvalid
	}
	if filter.To != nil && (*filter.To < 0 || *filter.To > maxUnixSecond) {
		return ListFilter{}, ErrInvalid
	}
	if filter.From != nil && filter.To != nil && *filter.From >= *filter.To {
		return ListFilter{}, ErrInvalid
	}
	if filter.UserID != nil && *filter.UserID <= 0 {
		return ListFilter{}, ErrInvalid
	}
	if !validOptionalText(filter.EndpointBaseURL, 4096, false) ||
		!validOptionalText(filter.UpstreamModel, 2048, true) ||
		!validOptionalText(filter.Model, 512, true) ||
		!validOptionalCode(filter.ErrorCode) {
		return ListFilter{}, ErrInvalid
	}
	switch role {
	case "user":
		if filter.UserID != nil || filter.EndpointBaseURL != nil || filter.UpstreamModel != nil {
			return ListFilter{}, ErrInvalid
		}
	case "admin":
		if filter.Model != nil {
			return ListFilter{}, ErrInvalid
		}
	case "steward":
		if filter.UserID != nil || filter.Model != nil {
			return ListFilter{}, ErrInvalid
		}
	default:
		return ListFilter{}, ErrInvalid
	}
	return filter, nil
}

func normalizeAttemptFilter(filter AttemptFilter) (AttemptFilter, error) {
	if filter.Limit == 0 {
		filter.Limit = defaultLimit
	}
	if filter.Limit < 1 || filter.Limit > maximumLimit || len(filter.Cursor) > 512 {
		return AttemptFilter{}, ErrInvalid
	}
	return filter, nil
}

func validOptionalText(value *string, maxBytes int, allowEmpty bool) bool {
	if value == nil {
		return true
	}
	return utf8.ValidString(*value) && len(*value) <= maxBytes && (allowEmpty || *value != "")
}

func validOptionalCode(value *string) bool {
	if value == nil {
		return true
	}
	if len(*value) < 1 || len(*value) > 64 {
		return false
	}
	for i := range *value {
		if (*value)[i] != '_' && ((*value)[i] < 'a' || (*value)[i] > 'z') && ((*value)[i] < '0' || (*value)[i] > '9') {
			return false
		}
	}
	return true
}

func (repository *Repository) cursorKey() ([]byte, error) {
	key, err := repository.cursorKeys.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil || len(key) != sha256.Size {
		clear(key)
		return nil, ErrUnavailable
	}
	return key, nil
}

func (repository *Repository) decodeListCursor(token, scope, owner string) (listCursor, error) {
	if token == "" {
		return listCursor{}, nil
	}
	key, err := repository.cursorKey()
	if err != nil {
		return listCursor{}, err
	}
	defer clear(key)
	now := repository.now().Unix()
	if now < 0 {
		return listCursor{}, ErrUnavailable
	}
	decoded, err := db.DecodePaginationCursorWithDerivedKey(key, token, scope, owner, uint64(now))
	if err != nil || len(decoded.Atoms) != 2 || decoded.Atoms[0].Kind != db.CursorUint ||
		decoded.Atoms[1].Kind != db.CursorUint || decoded.Atoms[0].Uint > uint64(maxUnixSecond) ||
		decoded.Atoms[1].Uint == 0 || decoded.Atoms[1].Uint > uint64(^uint64(0)>>1) {
		return listCursor{}, ErrInvalid
	}
	return listCursor{startedAt: int64(decoded.Atoms[0].Uint), rowID: int64(decoded.Atoms[1].Uint)}, nil
}

func (repository *Repository) encodeListCursor(scope, owner string, cursor listCursor) (string, error) {
	if cursor.startedAt < 0 || cursor.rowID <= 0 {
		return "", ErrInvalid
	}
	key, err := repository.cursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	now := repository.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return "", ErrUnavailable
	}
	token, err := db.EncodePaginationCursorWithDerivedKey(key, scope, owner, uint64(now+int64(cursorLifetime/time.Second)), []db.CursorAtom{
		{Kind: db.CursorUint, Uint: uint64(cursor.startedAt)},
		{Kind: db.CursorUint, Uint: uint64(cursor.rowID)},
	})
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func (repository *Repository) decodeAttemptCursor(token, scope, owner string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	key, err := repository.cursorKey()
	if err != nil {
		return 0, err
	}
	defer clear(key)
	now := repository.now().Unix()
	if now < 0 {
		return 0, ErrUnavailable
	}
	decoded, err := db.DecodePaginationCursorWithDerivedKey(key, token, scope, owner, uint64(now))
	if err != nil || len(decoded.Atoms) != 1 || decoded.Atoms[0].Kind != db.CursorUint ||
		decoded.Atoms[0].Uint > 100 {
		return 0, ErrInvalid
	}
	return int64(decoded.Atoms[0].Uint), nil
}

func (repository *Repository) encodeAttemptCursor(scope, owner string, sequence int64) (string, error) {
	if sequence < 1 || sequence > 100 {
		return "", ErrInvalid
	}
	key, err := repository.cursorKey()
	if err != nil {
		return "", err
	}
	defer clear(key)
	now := repository.now().Unix()
	if now < 0 || now > maxUnixSecond {
		return "", ErrUnavailable
	}
	token, err := db.EncodePaginationCursorWithDerivedKey(key, scope, owner, uint64(now+int64(cursorLifetime/time.Second)), []db.CursorAtom{
		{Kind: db.CursorUint, Uint: uint64(sequence)},
	})
	if err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func filterOwner(role string, actorID int64, filter ListFilter) string {
	hash := sha256.New()
	writePart := func(value string) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	writePart(role)
	writePart(strconv.FormatInt(actorID, 10))
	writePart(optionalInt64(filter.UserID))
	writePart(optionalString(filter.EndpointBaseURL))
	writePart(optionalString(filter.UpstreamModel))
	writePart(optionalString(filter.Model))
	writePart(optionalString(filter.ErrorCode))
	writePart(optionalInt(filter.Status))
	writePart(optionalInt64(filter.From))
	writePart(optionalInt64(filter.To))
	return role + ":" + hex.EncodeToString(hash.Sum(nil))
}

func attemptOwner(role string, actorID int64, requestID string) string {
	hash := sha256.Sum256([]byte(role + "\x00" + strconv.FormatInt(actorID, 10) + "\x00" + requestID))
	return role + ":" + hex.EncodeToString(hash[:])
}

func optionalString(value *string) string {
	if value == nil {
		return "-"
	}
	return "+" + *value
}

func optionalInt(value *int) string {
	if value == nil {
		return "-"
	}
	return "+" + strconv.Itoa(*value)
}

func optionalInt64(value *int64) string {
	if value == nil {
		return "-"
	}
	return "+" + strconv.FormatInt(*value, 10)
}

type commonLogRecord struct {
	rowID             int64
	id                string
	routeKind         string
	callerResultClass sql.NullString
	callerStatus      sql.NullInt64
	callerErrorCode   sql.NullString
	startedAt         int64
	completedAt       sql.NullInt64
	uncached          int64
	cacheWrite        int64
	cacheRead         int64
	output            int64
	usageUnknown      int
	attemptCount      int64
	chargeMagnitude   []byte
}

type rowScanner interface{ Scan(...any) error }

func scanCommon(scanner rowScanner, extra ...any) (commonLogRecord, error) {
	var record commonLogRecord
	targets := []any{
		&record.rowID, &record.id, &record.routeKind, &record.callerResultClass,
		&record.callerStatus, &record.callerErrorCode, &record.startedAt, &record.completedAt,
		&record.uncached, &record.cacheWrite, &record.cacheRead, &record.output,
		&record.usageUnknown, &record.attemptCount, &record.chargeMagnitude,
	}
	targets = append(targets, extra...)
	if err := scanner.Scan(targets...); err != nil {
		return commonLogRecord{}, err
	}
	if err := validateCommonRecord(record); err != nil {
		return commonLogRecord{}, err
	}
	return record, nil
}

func validateCommonRecord(record commonLogRecord) error {
	if record.rowID <= 0 || !db.ValidateOpaqueID(record.id, "req_") || record.startedAt < 0 ||
		record.startedAt > maxUnixSecond || record.attemptCount < 0 || record.attemptCount > 100 ||
		record.uncached < 0 || record.cacheWrite < 0 || record.cacheRead < 0 || record.output < 0 ||
		(record.usageUnknown != 0 && record.usageUnknown != 1) {
		return ErrInvariant
	}
	switch RouteKind(record.routeKind) {
	case RouteOpenAIChat, RouteCharityChat, RouteDiscovery:
	default:
		return ErrInvariant
	}
	if record.completedAt.Valid && (record.completedAt.Int64 < record.startedAt || record.completedAt.Int64 > maxUnixSecond) {
		return ErrInvariant
	}
	if record.callerResultClass.Valid {
		switch ResultClass(record.callerResultClass.String) {
		case ResultSuccess, ResultFailed, ResultCancelled:
		default:
			return ErrInvariant
		}
	}
	if record.callerStatus.Valid && (record.callerStatus.Int64 < 100 || record.callerStatus.Int64 > 599) {
		return ErrInvariant
	}
	if record.callerErrorCode.Valid && !validSafeCode(record.callerErrorCode.String) {
		return ErrInvariant
	}
	terminal := record.completedAt.Valid
	switch {
	case !record.callerResultClass.Valid:
		if record.callerStatus.Valid || record.callerErrorCode.Valid || terminal {
			return ErrInvariant
		}
	case record.callerResultClass.String == string(ResultSuccess):
		if !terminal || !record.callerStatus.Valid || record.callerStatus.Int64 < 200 ||
			record.callerStatus.Int64 > 399 || record.callerErrorCode.Valid {
			return ErrInvariant
		}
	case record.callerResultClass.String == string(ResultFailed):
		if !terminal || !record.callerStatus.Valid || record.callerStatus.Int64 < 400 || !record.callerErrorCode.Valid {
			return ErrInvariant
		}
	case record.callerResultClass.String == string(ResultCancelled):
		if !terminal || record.callerStatus.Valid || record.callerErrorCode.Valid {
			return ErrInvariant
		}
	default:
		return ErrInvariant
	}
	if _, err := db.DecodeU128(record.chargeMagnitude); err != nil {
		return ErrInvariant
	}
	return nil
}

func usageFromRecord(record commonLogRecord) (LogUsage, error) {
	return makeUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output,
		record.usageUnknown != 0, record.chargeMagnitude)
}

func makeUsage(uncached, cacheWrite, cacheRead, output int64, unknown bool, charge []byte) (LogUsage, error) {
	if uncached < 0 || cacheWrite < 0 || cacheRead < 0 || output < 0 {
		return LogUsage{}, ErrInvariant
	}
	wide, err := db.DecodeU128(charge)
	if err != nil {
		return LogUsage{}, ErrInvariant
	}
	total := new(big.Int).SetInt64(uncached)
	total.Add(total, big.NewInt(cacheWrite))
	total.Add(total, big.NewInt(cacheRead))
	total.Add(total, big.NewInt(output))
	return LogUsage{
		UncachedInputTokens:   strconv.FormatInt(uncached, 10),
		CacheWriteInputTokens: strconv.FormatInt(cacheWrite, 10),
		CacheReadInputTokens:  strconv.FormatInt(cacheRead, 10),
		OutputTokens:          strconv.FormatInt(output, 10), TotalTokens: total.String(),
		UsageUnknown: unknown, Charge: formatMilli(wide.Big()),
	}, nil
}

func zeroUsage(uncached, cacheWrite, cacheRead, output int64, unknown bool) (LogUsage, error) {
	return makeUsage(uncached, cacheWrite, cacheRead, output, unknown, db.EncodeU128(db.U128{}))
}

func formatMilli(value *big.Int) string {
	if value == nil || value.Sign() == 0 {
		return "0"
	}
	negative := value.Sign() < 0
	abs := new(big.Int).Abs(new(big.Int).Set(value))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(abs, big.NewInt(1000), remainder)
	result := whole.String()
	if remainder.Sign() != 0 {
		fraction := fmt.Sprintf("%03d", remainder.Int64())
		fraction = strings.TrimRight(fraction, "0")
		result += "." + fraction
	}
	if negative {
		return "-" + result
	}
	return result
}

func resultClassPointer(value sql.NullString) *ResultClass {
	if !value.Valid {
		return nil
	}
	result := ResultClass(value.String)
	return &result
}

func intPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func textPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func validSafeCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := range value {
		if value[i] != '_' && (value[i] < 'a' || value[i] > 'z') && (value[i] < '0' || value[i] > '9') {
			return false
		}
	}
	return true
}

func validUpstreamCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for i := range value {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func validDiagnostic(value string) bool {
	if !utf8.ValidString(value) || len(value) > 4096 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validConnector(value string) bool {
	return value == "openai-compatible" || value == "anthropic-compatible"
}

func validBaseURL(value string) bool {
	if !utf8.ValidString(value) || len(value) < 1 || len(value) > 4096 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func translateSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("logapi repository: %w", err)
	}
	return nil
}
