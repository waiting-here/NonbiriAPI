package logapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
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
	readDB     *sql.DB
	readAuth   ReadAuthorizer
	cursorKeys CursorKeyDeriver
	heldRead   AdminHeldReadAuthorizer
	now        func() time.Time
}

// ReadAuthorizer checks the current session or caller identity in the same
// snapshot as the rows it may read, after waiting for a connection.
type ReadAuthorizer interface {
	AuthorizeLogRead(context.Context, *sql.Tx, string, int64) error
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
) (*sql.Tx, bool, error) {
	if repository == nil {
		return nil, false, ErrInvalid
	}
	return repository.beginStewardReadOn(ctx, repository.db, stewardUserID, authorizer)
}

func (repository *Repository) beginStewardReadOn(
	ctx context.Context,
	database *sql.DB,
	stewardUserID int64,
	authorizer StewardAuthorizer,
) (*sql.Tx, bool, error) {
	if repository == nil || database == nil || ctx == nil || stewardUserID <= 0 || authorizer == nil {
		return nil, false, ErrInvalid
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, translateReadError(ctx, err)
	}
	redact, err := authorizer.AuthorizeStewardLogRead(ctx, tx, stewardUserID)
	if err != nil {
		_ = tx.Rollback()
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, false, translateReadError(ctx, err)
		}
		if errors.Is(err, authz.ErrMFARequired) {
			return nil, false, err
		}
		if errors.Is(err, ErrUnavailable) {
			return nil, false, ErrUnavailable
		}
		return nil, false, ErrForbidden
	}
	return tx, redact, nil
}

func (repository *Repository) authorizeStewardRead(
	ctx context.Context,
	stewardUserID int64,
	authorizer StewardAuthorizer,
) error {
	if repository == nil || ctx == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, logPageTimeout)
	defer cancel()
	tx, _, err := repository.beginStewardReadOn(ctx, repository.readDB, stewardUserID, authorizer)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Commit(); err != nil {
		return translateReadError(ctx, err)
	}
	return nil
}

func NewRepository(database *sql.DB, cursorKeys CursorKeyDeriver) (*Repository, error) {
	if database == nil || cursorKeys == nil {
		return nil, ErrInvalid
	}
	return &Repository{db: database, readDB: database, cursorKeys: cursorKeys, now: time.Now}, nil
}

// NewRepositoryWithReader keeps retention and held-log audit writes on the
// writer while ordinary browsing uses an independently bounded read pool.
func NewRepositoryWithReader(database, reader *sql.DB, cursorKeys CursorKeyDeriver, authorizer ReadAuthorizer) (*Repository, error) {
	if database == nil || reader == nil || cursorKeys == nil || authorizer == nil {
		return nil, ErrInvalid
	}
	return &Repository{db: database, readDB: reader, readAuth: authorizer, cursorKeys: cursorKeys, now: time.Now}, nil
}

func newRepository(database *sql.DB, cursorKeys CursorKeyDeriver, now func() time.Time) (*Repository, error) {
	if database == nil || cursorKeys == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Repository{db: database, readDB: database, cursorKeys: cursorKeys, now: now}, nil
}

type listCursor struct {
	startedAt int64
	rowID     int64
}

func normalizeListFilter(filter ListFilter, role string) (ListFilter, error) {
	if filter.Phase != "" && filter.Phase != "handler" && filter.Phase != "pre_handler" {
		return ListFilter{}, ErrInvalid
	}
	if filter.Page != nil {
		if !filter.Page.Valid() || filter.Cursor != "" || filter.Limit != 0 {
			return ListFilter{}, ErrInvalid
		}
		filter.Limit = filter.Page.Size
	}
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
	if filter.DiscordID != nil && !blacklist.ValidDiscordID(*filter.DiscordID) {
		return ListFilter{}, ErrInvalid
	}
	if filter.EndpointKeyID != nil && *filter.EndpointKeyID <= 0 {
		return ListFilter{}, ErrInvalid
	}
	if !validOptionalText(filter.EndpointBaseURL, 4096, false) ||
		!validOptionalText(filter.UpstreamModel, 2048, true) ||
		!validOptionalText(filter.Model, 512, true) ||
		!validOptionalText(filter.CharityModel, 512, false) ||
		!validOptionalCode(filter.ErrorCode) {
		return ListFilter{}, ErrInvalid
	}
	switch role {
	case "user":
		if filter.UserID != nil || filter.DiscordID != nil || filter.EndpointKeyID != nil || filter.EndpointBaseURL != nil || filter.UpstreamModel != nil || filter.CharityModel != nil || filter.UsageTotalMismatch != nil {
			return ListFilter{}, ErrInvalid
		}
	case "admin", "steward":
		if filter.Model != nil {
			return ListFilter{}, ErrInvalid
		}
	default:
		return ListFilter{}, ErrInvalid
	}
	return filter, nil
}

func normalizeAttemptFilter(filter AttemptFilter) (AttemptFilter, error) {
	if filter.Page != nil {
		if !filter.Page.Valid() || filter.Cursor != "" || filter.Limit != 0 {
			return AttemptFilter{}, ErrInvalid
		}
		filter.Limit = filter.Page.Size
	}
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
	writePart(optionalBool(filter.UsageTotalMismatch))
	writePart(optionalInt64(filter.From))
	writePart(optionalInt64(filter.To))
	if filter.Phase != "" {
		writePart(filter.Phase)
	}
	if filter.EndpointKeyID != nil {
		writePart("endpoint_key_id=" + optionalInt64(filter.EndpointKeyID))
	}
	if filter.CharityModel != nil {
		writePart("charity_model=" + optionalString(filter.CharityModel))
	}
	if filter.DiscordID != nil {
		writePart("discord_id=" + optionalString(filter.DiscordID))
	}
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

func optionalBool(value *bool) string {
	if value == nil {
		return "-"
	}
	if *value {
		return "+true"
	}
	return "+false"
}

func optionalInt64(value *int64) string {
	if value == nil {
		return "-"
	}
	return "+" + strconv.FormatInt(*value, 10)
}

type commonLogRecord struct {
	requestedStreaming                                          sql.NullBool
	automaticActions                                            sql.NullInt64
	rejectionDetail                                             *requestattempt.RejectionDetail
	origin                                                      OriginIdentity
	charityModel                                                *string
	charityModelID                                              *string
	rejectionStage, rejectionReason, requestMethod, requestPath sql.NullString
	rowID                                                       int64
	id                                                          string
	routeKind                                                   string
	callerResultClass                                           sql.NullString
	callerStatus                                                sql.NullInt64
	callerErrorCode                                             sql.NullString
	startedAt                                                   int64
	completedAt                                                 sql.NullInt64
	uncached                                                    int64
	cacheWrite                                                  int64
	cacheRead                                                   int64
	output                                                      int64
	usageUnknown                                                int
	usageTotalMismatch                                          int
	attemptCount                                                int64
	chargeMagnitude                                             []byte
	usageDetails                                                string
}

type rowScanner interface{ Scan(...any) error }

func scanCommon(scanner rowScanner, extra ...any) (commonLogRecord, error) {
	var record commonLogRecord
	targets := []any{
		&record.rowID, &record.id, &record.routeKind, &record.callerResultClass,
		&record.callerStatus, &record.callerErrorCode, &record.startedAt, &record.completedAt,
		&record.uncached, &record.cacheWrite, &record.cacheRead, &record.output,
		&record.usageUnknown, &record.usageTotalMismatch, &record.attemptCount, &record.chargeMagnitude,
		&record.rejectionStage, &record.rejectionReason, &record.requestMethod, &record.requestPath,
		&record.requestedStreaming, &record.automaticActions, &record.usageDetails,
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
		(record.usageUnknown != 0 && record.usageUnknown != 1) ||
		(record.usageTotalMismatch != 0 && record.usageTotalMismatch != 1) {
		return ErrInvariant
	}
	if !RouteKind(record.routeKind).Valid() {
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
	usage, err := makeUsage(record.uncached, record.cacheWrite, record.cacheRead, record.output,
		record.usageUnknown != 0, record.chargeMagnitude)
	if err != nil {
		return LogUsage{}, err
	}
	return addUsageDetails(usage, record.uncached, record.cacheWrite, record.cacheRead, record.output, record.usageDetails)
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

func zeroUsage(uncached, cacheWrite, cacheRead, output int64, unknown bool, details string) (LogUsage, error) {
	usage, err := makeUsage(uncached, cacheWrite, cacheRead, output, unknown, db.EncodeU128(db.U128{}))
	if err != nil {
		return LogUsage{}, err
	}
	return addUsageDetails(usage, uncached, cacheWrite, cacheRead, output, details)
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
	return connectorcontract.Type(value).Valid()
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

func translateReadError(ctx context.Context, err error) error {
	// SQLite can report SQLITE_INTERRUPT, and database/sql can roll a
	// transaction back, before the original context error reaches the caller.
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return translateSQLError(err)
}
