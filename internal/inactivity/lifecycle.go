package inactivity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Export struct {
	Activity *ActivityState `json:"activity"`
	Runs     []Run          `json:"runs"`
}

// ExportTx participates in the caller's authorized, consistent export snapshot.
func ExportTx(ctx context.Context, tx *sql.Tx, userID int64, limit int) (Export, error) {
	out := Export{Runs: make([]Run, 0)}
	if tx == nil || userID < 1 || limit < 1 || limit > 10000 {
		return out, ErrInvalid
	}
	a, err := readAccount(ctx, tx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Activity = &a.state
	rows, err := tx.QueryContext(ctx, `SELECT `+runColumns+` FROM inactivity_runs WHERE user_id=? ORDER BY created_at,id LIMIT ?`, userID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Runs) == limit {
			return out, ErrTooLarge
		}
		item, err := scanRun(rows)
		if err != nil {
			return out, err
		}
		out.Runs = append(out.Runs, item)
	}
	return out, rows.Err()
}

// DeleteTx runs before account deletion in the existing retirement transaction.
func DeleteTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	if tx == nil || userID < 1 {
		return ErrInvalid
	}
	for _, query := range []string{
		`UPDATE inactivity_runs SET user_id=NULL,ledger_operation_id=NULL WHERE user_id=?`,
		`UPDATE inactivity_audits SET actor_user_id=NULL WHERE actor_user_id=?`,
		`UPDATE inactivity_policy SET updated_by=NULL WHERE updated_by=?`,
		`DELETE FROM user_activity_state WHERE user_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, userID); err != nil {
			return err
		}
	}
	return nil
}

type HoldChecker func(context.Context, *sql.Tx, string, string, int64) (bool, error)
type RetentionResult struct {
	Processed    int
	Deidentified int
	Deleted      int
	More         bool
}

// Retain removes identity independently of an explicit hold on execution facts.
// The caller provides any applicable existing hold authority; no hold is implied.
func (s *Service) Retain(ctx context.Context, at int64, limit int, deadline time.Time, held HoldChecker) (RetentionResult, error) {
	var out RetentionResult
	if ctx == nil || at < 0 || at > maxTime || limit < 1 || limit > 100 || deadline.IsZero() {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`UPDATE inactivity_runs SET user_id=NULL,ledger_operation_id=NULL WHERE id IN (SELECT id FROM inactivity_runs WHERE user_id IS NOT NULL AND deidentify_at<=? ORDER BY deidentify_at,id LIMIT ?)`,
		`UPDATE inactivity_audits SET actor_user_id=NULL WHERE id IN (SELECT id FROM inactivity_audits WHERE actor_user_id IS NOT NULL AND deidentify_at<=? ORDER BY deidentify_at,id LIMIT ?)`,
	} {
		if out.Processed == limit {
			break
		}
		changed, err := tx.ExecContext(ctx, query, at, limit-out.Processed)
		if err != nil {
			return out, err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return out, err
		}
		out.Processed += int(n)
		out.Deidentified += int(n)
	}
	// Give identity removal a separate pass so the budget counts each fact once.
	if out.Processed == 0 {
		rows, err := tx.QueryContext(ctx, `SELECT 'inactivity_run',id,retain_until FROM inactivity_runs WHERE retain_until<=? UNION ALL SELECT 'inactivity_audit',id,retain_until FROM inactivity_audits WHERE retain_until<=? ORDER BY 3,1,2 LIMIT ?`, at, at, limit)
		if err != nil {
			return out, err
		}
		type candidate struct{ kind, id string }
		items := make([]candidate, 0, limit)
		for rows.Next() {
			var item candidate
			var until int64
			if err = rows.Scan(&item.kind, &item.id, &until); err != nil {
				_ = rows.Close()
				return out, err
			}
			items = append(items, item)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return out, err
		}
		if err = rows.Close(); err != nil {
			return out, err
		}
		for _, item := range items {
			out.Processed++
			if held != nil {
				yes, err := held(ctx, tx, item.kind, item.id, at)
				if err != nil {
					return out, err
				}
				if yes {
					continue
				}
			}
			table := "inactivity_runs"
			if item.kind == "inactivity_audit" {
				table = "inactivity_audits"
			}
			changed, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=?`, item.id)
			if err != nil {
				return out, err
			}
			n, err := changed.RowsAffected()
			if err != nil {
				return out, err
			}
			out.Deleted += int(n)
		}
	}
	out.More = out.Processed == limit
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
