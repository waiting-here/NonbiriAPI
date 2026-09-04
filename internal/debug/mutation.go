package debug

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// MutationRepository applies the shared Generation 2 control_mutation replay
// primitive to Debug's process-local state changes. Authentication and strict
// decoding happen in the HTTP adapter before every call, including replay.
type MutationRepository struct {
	db  *sql.DB
	now func() time.Time
	mu  sync.Mutex
}

func NewMutationRepository(database *sql.DB) (*MutationRepository, error) {
	return newMutationRepository(database, time.Now)
}

func newMutationRepository(database *sql.DB, now func() time.Time) (*MutationRepository, error) {
	if database == nil || now == nil {
		return nil, ErrInvalid
	}
	return &MutationRepository{db: database, now: now}, nil
}

func (repository *MutationRepository) Execute(
	ctx context.Context,
	userID int64,
	input resources.ControlMutation,
	operation func() (int, []byte, error),
) (int, []byte, bool, error) {
	if repository == nil || repository.db == nil || ctx == nil || userID <= 0 || operation == nil ||
		len(input.CanonicalBody) > idempotency.MaxControlBodyBytes {
		return 0, nil, false, ErrInvalid
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(userID, 10))
	if err != nil {
		return 0, nil, false, ErrInvalid
	}
	if _, err := idempotency.KeyHash(input.IdempotencyKey); err != nil {
		return 0, nil, false, ErrInvalid
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: input.Method, Route: input.Route,
		PathResourceIDs: input.PathIDs, Query: input.Query, Body: input.CanonicalBody,
	})
	if err != nil {
		return 0, nil, false, ErrInvalid
	}

	// Debug sessions are process-local and globally bounded. Serializing their
	// tiny control transactions also prevents two same-key requests from both
	// crossing the in-memory mutation boundary while SQLite arbitrates writers.
	repository.mu.Lock()
	defer repository.mu.Unlock()

	// The HTTP layer has already completed live authentication and strict
	// decoding. From this point onward a caller disconnect is response loss,
	// not authority to roll back the durable receipt after the process-local
	// operation has crossed its linearization point. database/sql otherwise
	// rolls back a BeginTx transaction when its parent context is cancelled,
	// which could leave the Hub changed without a replay record.
	durableCtx := context.WithoutCancel(ctx)
	tx, err := repository.db.BeginTx(durableCtx, nil)
	if err != nil {
		return 0, nil, false, ErrClosed
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	decision, err := idempotency.Begin(durableCtx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor,
		Key: input.IdempotencyKey, RequestHash: digest, DecisionNow: repository.now().Unix(),
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return 0, nil, false, ErrConflict
	}
	if err != nil {
		return 0, nil, false, ErrClosed
	}
	if decision.Kind == idempotency.Replay {
		if decision.HTTPStatus < 200 || decision.HTTPStatus > 299 ||
			len(decision.ResponseBody) > idempotency.MaxResponseBytes {
			return 0, nil, false, ErrClosed
		}
		if err := tx.Commit(); err != nil {
			return 0, nil, false, ErrClosed
		}
		committed = true
		return decision.HTTPStatus, append([]byte(nil), decision.ResponseBody...), true, nil
	}

	status, body, err := operation()
	if err != nil {
		return 0, nil, false, err
	}
	if status < 200 || status > 299 {
		return 0, nil, false, ErrInvalid
	}
	if len(body) > idempotency.MaxResponseBytes {
		return 0, nil, false, ErrCapacity
	}
	if err := idempotency.Complete(durableCtx, tx, decision, status, body); err != nil {
		return 0, nil, false, ErrClosed
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, false, ErrClosed
	}
	committed = true
	return status, append([]byte(nil), body...), false, nil
}
