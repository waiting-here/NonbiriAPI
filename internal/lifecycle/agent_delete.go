package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"sync"
)

func (coordinator *Coordinator) prepareDeletionTx(ctx context.Context, tx *sql.Tx, request DeleteRequest) ([]DeleteFinalizer, error) {
	userID, decisionNow := request.UserID, request.DecisionNow
	var err error
	var finalizers []DeleteFinalizer
	var actor *int64
	if request.ActorUserID > 0 {
		actor = &request.ActorUserID
	}
	request.Before, err = adminalerts.CaptureAccountDeletionTx(ctx, tx, userID, decisionNow, string(request.Source), actor)
	if err != nil {
		return finalizers, err
	}
	for _, adapter := range coordinator.delete.ordered() {
		finalizer, prepareErr := adapter.PrepareDelete(ctx, tx, request)
		if prepareErr != nil {
			return finalizers, prepareErr
		}
		finalizers = append(finalizers, finalizer)
	}
	if err := connectionstore.PrepareAccountDeletionTx(ctx, tx, userID, decisionNow); err != nil {
		return finalizers, err
	}
	operationID, err := coordinator.newID("op_")
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return finalizers, ErrUnavailable
	}
	if err := coordinator.ledger.ZeroAndDeleteAccount(ctx, tx, request, operationID); err != nil {
		return finalizers, err
	}
	return finalizers, nil
}

func finishDeleteFinalizers(items []DeleteFinalizer, committed bool) {
	if committed {
		for _, f := range items {
			if f != nil {
				f.Commit()
			}
		}
		return
	}
	for i := len(items) - 1; i >= 0; i-- {
		if items[i] != nil {
			items[i].Abort()
		}
	}
}

// AgentDeletion holds the same admission retirement used by account deletion.
// It must be prepared before opening the outer writer transaction.
type AgentDeletion struct {
	coordinator *Coordinator
	userID      int64
	retirement  Retirement
	finalizers  []DeleteFinalizer
	applied     bool
	once        sync.Once
}

func (c *Coordinator) PrepareAgentDeletion(ctx context.Context, userID int64) (*AgentDeletion, error) {
	if c == nil || ctx == nil || userID <= 0 {
		return nil, ErrInvalid
	}
	if c.closed.Load() {
		return nil, ErrClosed
	}
	retirement, err := c.retirement.BeginUserRetirement(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &AgentDeletion{coordinator: c, userID: userID, retirement: retirement}, nil
}
func (p *AgentDeletion) Apply(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, expected string, authorize agentplan.Authorize) error {
	if p == nil || tx == nil || p.applied || actor.Automatic || actor.Role != "admin" || authorize == nil {
		return ErrForbidden
	}
	if err := authorize(ctx, tx, actor, "users.delete"); err != nil {
		return err
	}
	if err := p.coordinator.adminAuth.AuthorizeAdmin(ctx, tx, actor.ID); err != nil {
		return err
	}
	if err := authorizeAgentDeleteTarget(ctx, tx, actor.ID, p.userID, expected); err != nil {
		return err
	}
	if err := authorize(ctx, tx, actor, "fresh_elevation"); err != nil {
		return err
	}
	p.applied = true
	var err error
	p.finalizers, err = p.coordinator.prepareDeletionTx(ctx, tx, DeleteRequest{UserID: p.userID, ActorUserID: actor.ID, Source: DeleteAdmin, DecisionNow: p.coordinator.now().Unix()})
	return err
}
func (p *AgentDeletion) Finalize(committed bool) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if committed && p.applied {
			p.retirement.Commit()
			finishDeleteFinalizers(p.finalizers, true)
		} else {
			finishDeleteFinalizers(p.finalizers, false)
			p.retirement.Abort()
		}
	})
}
func authorizeAgentDeleteTarget(ctx context.Context, tx *sql.Tx, actorID, userID int64, expected string) error {
	revision, err := db.ParseU128Decimal(expected)
	if err != nil {
		return ErrInvalid
	}
	var admin bool
	var raw []byte
	err = tx.QueryRowContext(ctx, "SELECT is_admin,revision FROM users WHERE id=?", userID).Scan(&admin, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if admin || actorID == userID {
		return ErrForbidden
	}
	current, err := db.DecodeU128(raw)
	if err != nil {
		return ErrInvariant
	}
	if current != revision {
		return ErrConflict
	}
	return nil
}
