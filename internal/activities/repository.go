package activities

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const maxUnixSecond = int64(253402300799)

type RepositoryConfig struct {
	Store          *db.Store
	UserFinalAuth  UserFinalTxAuthorizer
	AdminFinalAuth AdminFinalAuthorizer
	UserGate       UserMutationGate
	CursorKeys     CursorKeyDeriver
	Random         io.Reader
	Now            func() time.Time
	OperationID    func() (string, error)
	PeriodID       func() (string, error)
	ParticipantID  func() (string, error)
	PoolID         func() (string, error)
}

type Repository struct {
	db             *sql.DB
	userFinalAuth  UserFinalTxAuthorizer
	adminFinalAuth AdminFinalAuthorizer
	userGate       UserMutationGate
	cursors        cursorCodec
	random         io.Reader
	now            func() time.Time
	operationID    func() (string, error)
	periodID       func() (string, error)
	participantID  func() (string, error)
	poolID         func() (string, error)
}

func NewRepository(config RepositoryConfig) (*Repository, error) {
	if config.Store == nil || config.Store.DB() == nil || isNilInterface(config.UserFinalAuth) ||
		isNilInterface(config.AdminFinalAuth) || isNilInterface(config.UserGate) || isNilInterface(config.CursorKeys) {
		return nil, errors.New("activities: complete repository dependencies are required")
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
	if config.PeriodID == nil {
		config.PeriodID = func() (string, error) { return db.GenerateOpaqueID("thu_") }
	}
	if config.ParticipantID == nil {
		config.ParticipantID = func() (string, error) { return db.GenerateOpaqueID("thp_") }
	}
	if config.PoolID == nil {
		config.PoolID = func() (string, error) { return db.GenerateOpaqueID("pol_") }
	}
	return &Repository{
		db: config.Store.DB(), userFinalAuth: config.UserFinalAuth,
		adminFinalAuth: config.AdminFinalAuth, userGate: config.UserGate,
		cursors: cursorCodec{keys: config.CursorKeys}, random: config.Random,
		now: config.Now, operationID: config.OperationID, periodID: config.PeriodID,
		participantID: config.ParticipantID, poolID: config.PoolID,
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

func (r *Repository) decisionNow() (int64, error) {
	if r == nil || r.now == nil {
		return 0, ErrUnavailable
	}
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond-idempotency.ReplayWindowSeconds {
		return 0, ErrUnavailable
	}
	return now, nil
}

func (r *Repository) workerNow() (int64, error) {
	if r == nil || r.now == nil {
		return 0, ErrUnavailable
	}
	now := r.now().Unix()
	if now < 0 || now > maxUnixSecond {
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
		return nil, classifyDatabaseError("begin transaction", err)
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
		return classifyDatabaseError("commit transaction", err)
	}
	*committed = true
	return nil
}

func (r *Repository) beginUserMutation(ctx context.Context, userID int64) (*sql.Tx, error) {
	if r == nil || userID <= 0 {
		return nil, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	if err := classifyAuthorizationError(r.userFinalAuth.AuthorizeUserMutation(ctx, tx, userID)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := classifyAuthorizationError(r.userGate.AuthorizeUserActivity(ctx, tx, userID)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (r *Repository) beginAdminMutation(ctx context.Context, adminID int64) (*sql.Tx, error) {
	return r.beginAdminAuthorized(ctx, adminID, nil)
}

func (r *Repository) beginAdminRead(ctx context.Context, adminID int64) (*sql.Tx, error) {
	return r.beginAdminAuthorized(ctx, adminID, &sql.TxOptions{ReadOnly: true})
}

func (r *Repository) beginAdminAuthorized(ctx context.Context, adminID int64, options *sql.TxOptions) (*sql.Tx, error) {
	if r == nil {
		return nil, ErrInvalidRequest
	}
	if adminID <= 0 {
		return nil, ErrUnauthorized
	}
	if ctx == nil || r.db == nil {
		return nil, ErrUnavailable
	}
	tx, err := r.db.BeginTx(ctx, options)
	if err != nil {
		return nil, classifyDatabaseError("begin admin transaction", err)
	}
	if err := classifyAuthorizationError(r.adminFinalAuth.AuthorizeAdmin(ctx, tx, adminID)); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func generateCanonical(generator func() (string, error), prefix string) (string, error) {
	if generator == nil {
		return "", ErrUnavailable
	}
	id, err := generator()
	if err != nil || !db.ValidateOpaqueID(id, prefix) {
		return "", fmt.Errorf("%w: generate %s identifier", ErrUnavailable, prefix)
	}
	return id, nil
}

// stableOperationID derives the one immutable operation identity used by a
// delayed settlement. It contains no user identifier and is reproducible
// after process loss.
func stableOperationID(domain string, ids ...string) (string, error) {
	if domain == "" || len(ids) == 0 {
		return "", ErrInvariant
	}
	h := sha256.New()
	writeStableField(h, []byte("NonbiriAPI/activities-settlement-operation/v1"))
	writeStableField(h, []byte(domain))
	for _, id := range ids {
		if id == "" {
			return "", ErrInvariant
		}
		writeStableField(h, []byte(id))
	}
	sum := h.Sum(nil)
	id := "op_" + base64.RawURLEncoding.EncodeToString(sum[:16])
	if !db.ValidateOpaqueID(id, "op_") {
		return "", ErrInvariant
	}
	return id, nil
}

func writeStableField(writer io.Writer, value []byte) {
	var size [8]byte
	length := uint64(len(value))
	for i := 7; i >= 0; i-- {
		size[i] = byte(length)
		length >>= 8
	}
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}
