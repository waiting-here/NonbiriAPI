package attendance

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type SignInExport struct {
	PeriodID string `json:"period_id"`
	SiteDate string `json:"site_date"`
	SignedAt int64  `json:"signed_at"`
}
type UserExport struct {
	Periods []Period       `json:"periods"`
	SignIns []SignInExport `json:"sign_ins"`
}

func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user, at int64, limit int) (UserExport, error) {
	out := UserExport{Periods: []Period{}, SignIns: []SignInExport{}}
	if limit < 1 {
		return out, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, "SELECT period_id,site_date,signed_at FROM attendance_sign_ins WHERE user_id=? ORDER BY period_id,site_date LIMIT ?", user, limit+1)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var item SignInExport
		if err = rows.Scan(&item.PeriodID, &item.SiteDate, &item.SignedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.SignIns = append(out.SignIns, item)
		if len(ids) == 0 || ids[len(ids)-1] != item.PeriodID {
			ids = append(ids, item.PeriodID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.SignIns) > limit {
		return out, ErrExportLimit
	}
	// Each award requires one sign-in, so the same collection limit also bounds
	// award receipts without an unbounded historical count or a second sweep.
	encoded, err := json.Marshal(out.SignIns)
	if err != nil {
		return out, err
	}
	bytes := len(encoded)
	for _, id := range ids {
		p, e := readPeriod(ctx, tx, id)
		if e != nil {
			return out, e
		}
		p, e = viewTx(ctx, tx, p, user, at, false)
		if e != nil {
			return out, e
		}
		encoded, e = json.Marshal(p)
		if e != nil {
			return out, e
		}
		bytes += len(encoded)
		if bytes > lifecycle.MaxExportBytes {
			return out, ErrExportLimit
		}
		out.Periods = append(out.Periods, p)
	}
	return out, nil
}

// Child daily facts and awards cascade in the account deletion transaction.
func DeleteUserTx(ctx context.Context, tx *sql.Tx, user int64) error {
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(user, 10))
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM idempotency_records WHERE scope=? AND actor_scope_hash=?", idempotency.ScopeActivity, actor[:]); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM attendance_progress WHERE user_id=?", user)
	return err
}
