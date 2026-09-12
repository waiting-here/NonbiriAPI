package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type linkLinkPort struct{ onboarding }

func (port linkLinkPort) Entry(ctx context.Context, tx *sql.Tx, input ports.Entry) error {
	if !validEntry(ctx, tx, input, "ll_") || input.Meta.ActorUserID != input.UserID {
		return ledger.ErrInvalidPlan
	}
	var userID, price, gamePaid int64
	var version int
	var operation, state, spec string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,price_milli,operation_id,state,rules_version,game_paid_milli,spec FROM game_linklink_sessions WHERE id=?`, input.ResourceID).Scan(&userID, &price, &operation, &state, &version, &gamePaid, &spec); err != nil {
		return err
	}
	if userID != input.UserID || operation != input.Meta.OperationID || state != "active" || input.Amount.Big().Cmp(big.NewInt(price)) != 0 || input.GamePaid.Big().Cmp(big.NewInt(gamePaid)) != 0 {
		return ledger.ErrInvalidPlan
	}
	if version == 2 {
		wallets, err := walletAccounts(ctx, tx, input.UserID)
		if err != nil {
			return err
		}
		platforms, err := codedAccounts(ctx, tx, "platform")
		if err != nil {
			return err
		}
		payment, err := entryPayment(input)
		if err != nil {
			return err
		}
		plan, err := ledger.NewLinkLinkEntryWithPayment(input.Meta, input.ResourceID, wallets, platforms, payment)
		if err != nil {
			return err
		}
		if _, err = ledger.Apply(ctx, tx, plan); err != nil {
			return err
		}
		return port.reserve(ctx, tx, input.UserID, spec, onboardingParent{column: "linklink_session_id", id: input.ResourceID}, input.Meta.CreatedAt)
	}
	user, err := ledger.UserAccount(ctx, tx, input.UserID)
	if err != nil {
		return err
	}
	platform, err := ledger.CodedAccount(ctx, tx, "platform")
	if err != nil {
		return err
	}
	plan, err := ledger.NewLinkLinkEntry(input.Meta, input.ResourceID, user.ID, platform.ID, input.Amount)
	if err != nil {
		return err
	}
	_, err = ledger.Apply(ctx, tx, plan)
	return err
}

func (port linkLinkPort) Terminal(ctx context.Context, tx *sql.Tx, sessionID string, userID, now int64) error {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(sessionID, "ll_") || userID <= 0 || now < 0 || now > 253402300799 {
		return ledger.ErrInvalidPlan
	}
	var spec, reason string
	var version int
	var terminalAt int64
	if err := tx.QueryRowContext(ctx, `SELECT a.spec,a.rules_version,s.terminal_reason,s.terminal_at
FROM game_linklink_sessions a JOIN game_linklink_summaries s ON s.session_id=a.id AND s.user_id=a.user_id
WHERE a.id=? AND a.user_id=? AND a.rules_version=s.rules_version AND a.spec=s.spec`, sessionID, userID).Scan(&spec, &version, &reason, &terminalAt); err != nil {
		return err
	}
	if terminalAt != now {
		return ledger.ErrInvalidPlan
	}
	if version == 1 {
		return nil
	}
	parent := onboardingParent{column: "linklink_session_id", id: sessionID}
	switch reason {
	case "completed":
		return port.complete(ctx, tx, userID, spec, parent, now)
	case "timed_out", "abandoned":
		return port.release(ctx, tx, parent)
	default:
		return ledger.ErrInvalidPlan
	}
}

func (port linkLinkPort) ReleaseOnboarding(ctx context.Context, tx *sql.Tx, userID int64) error {
	if ctx == nil || tx == nil || userID <= 0 {
		return ledger.ErrInvalidPlan
	}
	return port.releaseUser(ctx, tx, userID)
}
