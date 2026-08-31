package issues

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	maxUnixSecond       = int64(253402300799)
	defaultPageLimit    = 50
	maxPageLimit        = 100
	maxCurrentPerUser   = int64(4096)
	maxClosedPerUser    = int64(1000)
	closedRetention     = int64(90 * 24 * 60 * 60)
	maxSafeDetailRunes  = 256
	maxRebuildCursorLen = 2048
)

type Config struct {
	Store              *db.Store
	CursorKeys         CursorKeyDeriver
	ResourceValidation ResourceValidationAuthority
	Now                func() time.Time
	IssueID            func() (string, error)
}

type Repository struct {
	db                 *sql.DB
	cursors            cursorCodec
	resourceValidation ResourceValidationAuthority
	now                func() time.Time
	newID              func() (string, error)
}

type Service struct {
	repository *Repository
	sources    *SourceAdapter
}

type SourceAdapter struct {
	repository *Repository
}

func NewRepository(config Config) (*Repository, error) {
	if config.Store == nil || config.Store.DB() == nil || isNilInterface(config.CursorKeys) || isNilInterface(config.ResourceValidation) {
		return nil, errors.New("issues: complete dependencies are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.IssueID == nil {
		config.IssueID = func() (string, error) { return db.GenerateOpaqueID("iss_") }
	}
	return &Repository{
		db: config.Store.DB(), cursors: cursorCodec{keys: config.CursorKeys},
		resourceValidation: config.ResourceValidation, now: config.Now, newID: config.IssueID,
	}, nil
}

func NewService(repository *Repository) (*Service, error) {
	if repository == nil || repository.db == nil {
		return nil, errors.New("issues: repository is required")
	}
	service := &Service{repository: repository}
	service.sources = &SourceAdapter{repository: repository}
	return service, nil
}

func (service *Service) Sources() *SourceAdapter {
	if service == nil {
		return nil
	}
	return service.sources
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
	if now < 0 || now > maxUnixSecond-closedRetention {
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
		return nil, fmt.Errorf("issues: begin transaction: %w", err)
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
		return fmt.Errorf("issues: commit transaction: %w", err)
	}
	*committed = true
	return nil
}

type rawIssue struct {
	id, resourceRef, summaryCode, safeDetail       string
	userID, generation, firstSeen, lastSeen, count int64
	source                                         Source
	resourceKind                                   ResourceKind
	rootCause                                      RootCause
	state                                          string
	deepLinkKind, deepLinkRef                      sql.NullString
	closedAt, retainUntil                          sql.NullInt64
}

type rowScanner interface {
	Scan(...any) error
}

func scanIssue(scanner rowScanner) (rawIssue, error) {
	var issue rawIssue
	var source, resourceKind, rootCause string
	err := scanner.Scan(
		&issue.id, &issue.userID, &source, &resourceKind, &issue.resourceRef, &rootCause, &issue.generation,
		&issue.state, &issue.summaryCode, &issue.safeDetail, &issue.deepLinkKind, &issue.deepLinkRef,
		&issue.firstSeen, &issue.lastSeen, &issue.count, &issue.closedAt, &issue.retainUntil,
	)
	if err != nil {
		return rawIssue{}, err
	}
	issue.source, issue.resourceKind, issue.rootCause = Source(source), ResourceKind(resourceKind), RootCause(rootCause)
	if !issue.valid() {
		return rawIssue{}, ErrUnavailable
	}
	return issue, nil
}

const issueSelectColumns = `
id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,
deep_link_kind,deep_link_ref,first_seen_at,last_seen_at,count,closed_at,retain_until`

func (issue rawIssue) valid() bool {
	if !validateIssueID(issue.id) || issue.userID <= 0 || !validTuple(issue.source, issue.resourceKind, issue.rootCause) ||
		!validDecimalResource(issue.resourceRef) || issue.generation < 1 || issue.firstSeen < 0 || issue.firstSeen > issue.lastSeen ||
		issue.lastSeen > maxUnixSecond || issue.count < 1 || issue.summaryCode != string(issue.rootCause) || !validSafeDetail(issue.safeDetail) {
		return false
	}
	if issue.deepLinkKind.Valid != issue.deepLinkRef.Valid {
		return false
	}
	if issue.deepLinkKind.Valid && (ResourceKind(issue.deepLinkKind.String) != issue.resourceKind || !validDecimalResource(issue.deepLinkRef.String)) {
		return false
	}
	switch issue.state {
	case "current":
		return !issue.closedAt.Valid && !issue.retainUntil.Valid
	case "closed":
		return issue.closedAt.Valid && issue.retainUntil.Valid && issue.retainUntil.Int64 == issue.closedAt.Int64+closedRetention
	default:
		return false
	}
}

func validateIssueID(value string) bool { return db.ValidateOpaqueID(value, "iss_") }

func validTuple(source Source, resource ResourceKind, root RootCause) bool {
	switch source {
	case SourceModelDiscovery:
		return resource == ResourceEndpointKey && root == RootDiscoveryFailed
	case SourceRoutingProjection:
		return resource == ResourceModel && root == RootNoRoutableBinding
	case SourceResourceValidator:
		return (resource == ResourceEndpoint || resource == ResourceEndpointKey) &&
			(root == RootCredentialInvalid || root == RootConfigurationInvalid)
	default:
		return false
	}
}

func validDecimalResource(value string) bool {
	if value == "" || value == "0" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := range value {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}

func validSafeDetail(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxSafeDetailRunes || len(value) > 1024 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x7f {
			return false
		}
	}
	return true
}

func (repository *Repository) allocateID(ctx context.Context, tx *sql.Tx) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := repository.newID()
		if err != nil {
			return "", fmt.Errorf("issues: generate identifier: %w", err)
		}
		if !validateIssueID(id) {
			return "", ErrUnavailable
		}
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_issues WHERE id=?)`, id).Scan(&exists); err != nil {
			return "", fmt.Errorf("issues: check identifier collision: %w", err)
		}
		if exists == 0 {
			return id, nil
		}
	}
	return "", ErrUnavailable
}

type ownerProjection struct {
	userID      int64
	deepLinkRef string
}

func readOwnerProjection(ctx context.Context, tx *sql.Tx, expectedUserID int64, kind ResourceKind, resourceID int64) (ownerProjection, error) {
	if tx == nil || expectedUserID <= 0 || resourceID <= 0 {
		return ownerProjection{}, ErrNotFound
	}
	var owner ownerProjection
	switch kind {
	case ResourceEndpoint:
		err := tx.QueryRowContext(ctx, `SELECT user_id,CAST(id AS TEXT) FROM endpoints WHERE id=?`, resourceID).Scan(&owner.userID, &owner.deepLinkRef)
		if errors.Is(err, sql.ErrNoRows) {
			return ownerProjection{}, ErrNotFound
		}
		if err != nil {
			return ownerProjection{}, fmt.Errorf("issues: read endpoint owner: %w", err)
		}
	case ResourceEndpointKey:
		err := tx.QueryRowContext(ctx, `
SELECT e.user_id,CAST(e.id AS TEXT) FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?`, resourceID).Scan(&owner.userID, &owner.deepLinkRef)
		if errors.Is(err, sql.ErrNoRows) {
			return ownerProjection{}, ErrNotFound
		}
		if err != nil {
			return ownerProjection{}, fmt.Errorf("issues: read endpoint key owner: %w", err)
		}
	case ResourceModel:
		err := tx.QueryRowContext(ctx, `SELECT user_id,CAST(id AS TEXT) FROM models WHERE id=?`, resourceID).Scan(&owner.userID, &owner.deepLinkRef)
		if errors.Is(err, sql.ErrNoRows) {
			return ownerProjection{}, ErrNotFound
		}
		if err != nil {
			return ownerProjection{}, fmt.Errorf("issues: read model owner: %w", err)
		}
	default:
		return ownerProjection{}, ErrInvalidRequest
	}
	if owner.userID != expectedUserID || !validDecimalResource(owner.deepLinkRef) {
		return ownerProjection{}, ErrNotFound
	}
	return owner, nil
}

func reportReasonActive(ctx context.Context, tx *sql.Tx, keyID int64) (bool, error) {
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM endpoint_key_suspensions WHERE endpoint_key_id=? AND reason_type='report_case'
)`, keyID).Scan(&active); err != nil {
		return false, fmt.Errorf("issues: read report reason: %w", err)
	}
	return active == 1, nil
}

func (repository *Repository) scrubReference(issueID string) (string, error) {
	key, err := repository.cursors.derive("issue-resource-scrub/v1")
	if err != nil {
		return "", err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(issueID))
	sum := mac.Sum(nil)
	value := binary.BigEndian.Uint64(sum[:8]) & uint64(^uint64(0)>>1)
	clear(sum)
	if value == 0 {
		value = 1
	}
	return strconv.FormatUint(value, 10), nil
}

func normalizedLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}

func validLimit(limit int) bool { return limit == 0 || (limit >= 1 && limit <= maxPageLimit) }

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copyValue := value.Int64
	return &copyValue
}
