package finance

import (
	"context"
	"database/sql"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// Parent columns are selected only by the compiled game adapters.
type onboardingParent struct {
	column string
	id     string
	seat   int
}

func (parent onboardingParent) predicate() (string, []any) {
	if parent.column == "rps_session_id" {
		return "rps_session_id=? AND seat_no=?", []any{parent.id, parent.seat}
	}
	return parent.column + "=?", []any{parent.id}
}

type onboarding struct{ module game.ModuleDescriptor }

func (port onboarding) reserve(ctx context.Context, tx *sql.Tx, userID int64, task string, parent onboardingParent, now int64) error {
	if _, err := port.module.OnboardingReward(task); err != nil {
		return ledger.ErrInvalidPlan
	}
	var completed bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM game_onboarding_completions WHERE user_id=? AND game_key=? AND task_key=?)", userID, port.module.ID, task).Scan(&completed); err != nil {
		return err
	}
	if completed {
		return nil
	}
	id, err := db.GenerateOpaqueID("goh_")
	if err != nil {
		return err
	}
	ref, err := ledger.GameOnboardingHold(id)
	if err != nil {
		return err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	return ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO game_onboarding_holds(id,user_id,game_key,task_key,ledger_rows_remaining,created_at,"+parent.column+") VALUES(?,?,?,?,?,?,?)",
			id, userID, port.module.ID, task, db.EncodeU128(one), now, parent.id)
		return err
	})
}

// complete is called only after the game adapter has checked its authoritative
// success event. It shares the outer transaction with the ordinary settlement.
// Ledger operations run sequentially so each observes the updated capacity CAS.
func (port onboarding) complete(ctx context.Context, tx *sql.Tx, userID int64, task string, parent onboardingParent, now int64) error {
	award, err := port.module.OnboardingReward(task)
	if err != nil {
		return ledger.ErrInvalidPlan
	}
	where, args := parent.predicate()
	args = append(args, userID, port.module.ID, task, userID, port.module.ID, task)
	var holdID sql.NullString
	var completed bool
	if err := tx.QueryRowContext(ctx, "SELECT (SELECT id FROM game_onboarding_holds WHERE "+where+" AND user_id=? AND game_key=? AND task_key=?), EXISTS(SELECT 1 FROM game_onboarding_completions WHERE user_id=? AND game_key=? AND task_key=?)", args...).Scan(&holdID, &completed); err != nil {
		return err
	}
	if completed {
		if holdID.Valid {
			return releaseOnboardingHold(ctx, tx, holdID.String)
		}
		return nil
	}
	if !holdID.Valid {
		return ledger.ErrInvalidReservation
	}
	var wallet, external int64
	if err := tx.QueryRowContext(ctx, `SELECT u.id,e.id FROM credit_accounts u CROSS JOIN credit_accounts e
WHERE u.kind='user' AND u.user_id=? AND u.asset_type='general' AND e.code='external' AND e.asset_type='general'`, userID).Scan(&wallet, &external); err != nil {
		return err
	}
	operationID, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return err
	}
	plan, err := ledger.NewGameOnboardingReward(ledger.Meta{OperationID: operationID, ActorUserID: userID, CreatedAt: now},
		holdID.String, wallet, external, ledger.AmountFromMilli(award))
	if err != nil {
		return err
	}
	ref, err := ledger.GameOnboardingHold(holdID.String)
	if err != nil {
		return err
	}
	if _, err := ledger.ConsumeReserved(ctx, tx, ref, plan, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM game_onboarding_holds WHERE id=?", holdID.String)
		return err
	}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO game_onboarding_completions(user_id,game_key,task_key,award_milli,operation_id,completed_at) VALUES(?,?,?,?,?,?)",
		userID, port.module.ID, task, award, operationID, now)
	return err
}

func (port onboarding) release(ctx context.Context, tx *sql.Tx, parent onboardingParent) error {
	where, args := parent.predicate()
	args = append(args, port.module.ID)
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM game_onboarding_holds WHERE "+where+" AND game_key=?", args...).Scan(&id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return releaseOnboardingHold(ctx, tx, id)
}

func (port onboarding) releaseUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT id FROM game_onboarding_holds WHERE user_id=? AND game_key=? ORDER BY id", userID, port.module.ID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := releaseOnboardingHold(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func releaseOnboardingHold(ctx context.Context, tx *sql.Tx, id string) error {
	ref, err := ledger.GameOnboardingHold(id)
	if err != nil {
		return err
	}
	one, _ := db.U128FromBig(big.NewInt(1))
	return ledger.ReleaseReserved(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM game_onboarding_holds WHERE id=?", id)
		return err
	})
}
