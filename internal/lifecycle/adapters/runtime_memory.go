package adapters

import (
	"context"
	"database/sql"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/accountstream"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type accountStreamForgetter interface {
	ForgetAccounts(context.Context, []int64) error
}

type debugForgetter interface {
	ForgetAccount(int64) error
}

// RuntimeMemoryDeleteAdapter owns the final deletion slot. Preparing it has
// no process-local side effect; the returned finalizer performs the frozen
// account-stream then Debug cleanup only after the shared transaction commits.
type RuntimeMemoryDeleteAdapter struct {
	accountStream accountStreamForgetter
	debug         debugForgetter
}

func NewRuntimeMemoryDeleteAdapter(
	accountEvents *accountstream.Hub,
	debugHub *debug.Hub,
) (*RuntimeMemoryDeleteAdapter, error) {
	if accountEvents == nil || debugHub == nil {
		return nil, lifecycle.ErrInvalid
	}
	return newRuntimeMemoryDeleteAdapter(accountEvents, debugHub)
}

func newRuntimeMemoryDeleteAdapter(
	accountEvents accountStreamForgetter,
	debugHub debugForgetter,
) (*RuntimeMemoryDeleteAdapter, error) {
	if accountEvents == nil || debugHub == nil {
		return nil, lifecycle.ErrInvalid
	}
	return &RuntimeMemoryDeleteAdapter{accountStream: accountEvents, debug: debugHub}, nil
}

func (adapter *RuntimeMemoryDeleteAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.accountStream == nil || adapter.debug == nil ||
		!validDeleteCall(ctx, tx, request) {
		return nil, lifecycle.ErrInvalid
	}
	return &runtimeMemoryDeleteFinalizer{
		accountStream: adapter.accountStream,
		debug:         adapter.debug,
		userID:        request.UserID,
	}, nil
}

type runtimeMemoryDeleteFinalizer struct {
	accountStream accountStreamForgetter
	debug         debugForgetter
	userID        int64
	done          atomic.Bool
}

func (finalizer *runtimeMemoryDeleteFinalizer) Commit() bool {
	if finalizer == nil || finalizer.accountStream == nil || finalizer.debug == nil ||
		!finalizer.done.CompareAndSwap(false, true) {
		return false
	}
	// Post-commit cleanup must not inherit a caller disconnect. Both owners are
	// process-local and idempotent; an already-closed owner needs no DB rollback.
	_ = finalizer.accountStream.ForgetAccounts(context.Background(), []int64{finalizer.userID})
	_ = finalizer.debug.ForgetAccount(finalizer.userID)
	return true
}

func (finalizer *runtimeMemoryDeleteFinalizer) Abort() bool {
	return finalizer != nil && finalizer.done.CompareAndSwap(false, true)
}

var _ lifecycle.DeleteAdapter = (*RuntimeMemoryDeleteAdapter)(nil)
