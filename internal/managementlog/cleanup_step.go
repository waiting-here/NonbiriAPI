package managementlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Step removes at most 512 detail rows and 32 operation heads. The current head
// is persisted before removing details, so even a very large batch is resumable.
func (s *Service) Step(ctx context.Context) (active bool, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	var jobID int64
	defer func() {
		_ = tx.Rollback()
		if err != nil && ctx.Err() == nil && jobID > 0 {
			s.failCleanup(ctx, jobID)
		}
	}()
	// Serialize worker claims before selecting a job.
	_, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET updated_at=updated_at WHERE id=(SELECT id FROM management_operation_cleanup_jobs WHERE state IN ('pending','running') ORDER BY id LIMIT 1)")
	if err != nil {
		return false, err
	}
	job, err := readJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM management_operation_cleanup_jobs WHERE state IN ('pending','running') ORDER BY id LIMIT 1"))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	jobID = job.ID
	_, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET state='running',updated_at=? WHERE id=?", nowUnix(), job.ID)
	if err != nil {
		return true, err
	}
	remaining := 512
	for heads := 0; heads < 32 && remaining > 0; heads++ {
		id := job.current
		if id == 0 {
			where, args := filterSQL(job.Filter, "o", false)
			err = tx.QueryRowContext(ctx, "SELECT o.id FROM management_operations o WHERE "+where+" AND o.id>? AND o.id<=? ORDER BY o.id LIMIT 1", append(args, job.after, job.CutoffMaxID)...).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				if err = s.finishCleanupTx(ctx, tx, &job, Succeeded, "completed"); err != nil {
					return true, err
				}
				return true, tx.Commit()
			}
			if err != nil {
				return true, err
			}
			var activeTask bool
			if err = tx.QueryRowContext(ctx, "SELECT cleaning=1 OR (outcome='accepted' AND correlation_kind<>'' AND correlation_id<>'') FROM management_operations WHERE id=?", id).Scan(&activeTask); err != nil {
				return true, err
			}
			if activeTask {
				job.SkippedActive++
				job.after = id
				continue
			}
			if _, err = tx.ExecContext(ctx, "UPDATE management_operations SET cleaning=1 WHERE id=?", id); err != nil {
				return true, err
			}
			job.current = id
			if _, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET current_operation_id=? WHERE id=?", id, job.ID); err != nil {
				return true, err
			}
		}
		result, e := tx.ExecContext(ctx, `DELETE FROM management_operation_items WHERE operation_id=? AND ordinal IN (SELECT ordinal FROM management_operation_items WHERE operation_id=? ORDER BY ordinal LIMIT ?)`, id, id, remaining)
		if e != nil {
			return true, e
		}
		removed, e := result.RowsAffected()
		if e != nil {
			return true, e
		}
		remaining -= int(removed)
		job.DeletedItems += removed
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM management_operation_items WHERE operation_id=? LIMIT 1)", id).Scan(&exists); err != nil {
			return true, err
		}
		if exists {
			break
		}
		_, err = tx.ExecContext(ctx, `UPDATE management_operation_rollups SET
 operations=operations-1,
 targets=targets-(SELECT targets FROM management_operations WHERE id=?),
 succeeded=succeeded-(SELECT succeeded FROM management_operations WHERE id=?),
 skipped=skipped-(SELECT skipped FROM management_operations WHERE id=?),
 failed=failed-(SELECT failed FROM management_operations WHERE id=?)
 WHERE group_key=(SELECT group_key FROM management_operations WHERE id=?)`, id, id, id, id, id)
		if err != nil {
			return true, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM management_operation_rollups WHERE group_key=(SELECT group_key FROM management_operations WHERE id=?) AND operations=0", id); err != nil {
			return true, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM management_operations WHERE id=?", id); err != nil {
			return true, err
		}
		job.DeletedOperations++
		job.after = id
		job.current = 0
	}
	_, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET current_operation_id=?,after_operation_id=?,skipped_active=?,deleted_operations=?,deleted_items=?,updated_at=? WHERE id=?", job.current, job.after, job.SkippedActive, job.DeletedOperations, job.DeletedItems, nowUnix(), job.ID)
	if err != nil {
		return true, err
	}
	return true, tx.Commit()
}
func (s *Service) finishCleanupTx(ctx context.Context, tx *sql.Tx, job *CleanupJob, outcome Outcome, state string) error {
	meta := Metadata{EventID: newEventID(), CreatedAt: nowUnix(), Source: SourceWeb, ExecutorKind: "system", Actor: job.Actor, Authorizer: job.Actor, Domain: "management-operations", Action: "cleanup_finish", CorrelationKind: "cleanup_job", CorrelationID: fmt.Sprint(job.ID)}
	var startID int64
	if err := tx.QueryRowContext(ctx, "SELECT start_operation_id FROM management_operation_cleanup_jobs WHERE id=?", job.ID).Scan(&startID); err != nil {
		return err
	}
	original, err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM management_operations WHERE id=?", startID))
	if err == nil {
		meta.Source = original.Source
		meta.Authorizer = original.Authorizer
		meta.AuthorizationRef = original.AuthorizationRef
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	meta.Summary = fmt.Sprintf("已清理 %d 条操作及 %d 条明细，截止记录 %d；保留 %d 个进行中的任务", job.DeletedOperations, job.DeletedItems, job.CutoffMaxID, job.SkippedActive)
	if err = AppendRelatedTx(ctx, tx, "cleanup_job", fmt.Sprint(job.ID), Item{Action: "cleanup_finish", TargetKind: "cleanup_job", TargetID: fmt.Sprint(job.ID), Outcome: outcome, Code: state}, true); err != nil {
		return err
	}
	id, err := RecordTx(ctx, tx, Record{Metadata: meta, Result: Result{Outcome: outcome, Code: state, Items: []Item{{Action: "cleanup_finish", TargetKind: "cleanup_job", TargetID: fmt.Sprint(job.ID), Outcome: outcome, Code: state}}}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET state=?,updated_at=?,deleted_operations=?,deleted_items=?,finish_operation_id=?,error_code=?,current_operation_id=?,after_operation_id=?,skipped_active=? WHERE id=?", state, nowUnix(), job.DeletedOperations, job.DeletedItems, id, job.ErrorCode, job.current, job.after, job.SkippedActive, job.ID)
	return err
}
func (s *Service) failCleanup(ctx context.Context, id int64) {
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2e9)
	defer cancel()
	tx, err := s.DB.BeginTx(work, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	job, err := readJob(tx.QueryRowContext(work, "SELECT "+jobColumns+" FROM management_operation_cleanup_jobs WHERE id=? AND state IN ('pending','running')", id))
	if err != nil {
		return
	}
	job.ErrorCode = "storage_error"
	if s.finishCleanupTx(work, tx, &job, Failed, "failed") == nil {
		_ = tx.Commit()
	}
}
