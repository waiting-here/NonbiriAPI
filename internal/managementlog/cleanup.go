package managementlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type CleanupRequest struct {
	Filter    Filter `json:"filter"`
	All       bool   `json:"all"`
	RequestID string `json:"request_id"`
}
type PreparedCleanup struct {
	Filter      Filter `json:"filter"`
	All         bool   `json:"all"`
	CutoffMaxID int64  `json:"cutoff_max_id,string"`
}
type CleanupJob struct {
	ID                int64   `json:"id,string"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
	Actor             Subject `json:"actor"`
	Filter            Filter  `json:"filter"`
	CutoffMaxID       int64   `json:"cutoff_max_id,string"`
	State             string  `json:"state"`
	DeletedOperations int64   `json:"deleted_operations,string"`
	DeletedItems      int64   `json:"deleted_items,string"`
	SkippedActive     int64   `json:"skipped_active,string"`
	ErrorCode         string  `json:"error_code"`
	current           int64
	after             int64
}

func (s *Service) PrepareCleanup(ctx context.Context, adminID int64, request CleanupRequest) (PreparedCleanup, error) {
	prepared := PreparedCleanup{Filter: request.Filter, All: request.All}
	if err := validateFilter(request.Filter); err != nil {
		return prepared, err
	}
	if request.All && request.Filter != (Filter{}) || !request.All && request.Filter == (Filter{}) {
		return prepared, ErrInvalid
	}
	tx, err := s.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return prepared, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, adminID); err != nil {
		return prepared, err
	}
	err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM management_operations").Scan(&prepared.CutoffMaxID)
	return prepared, err
}

// ApplyCleanupTx is shared by the HTTP confirmation and approved AI plans.
// The fixed cutoff is prepared before the confirmation/plan approval.
func (s *Service) ApplyCleanupTx(ctx context.Context, tx *sql.Tx, adminID int64, requestID string, prepared PreparedCleanup) (CleanupJob, error) {
	if tx == nil || requestID == "" || !tokenOK(requestID, 128) || prepared.CutoffMaxID < 0 {
		return CleanupJob{}, ErrInvalid
	}
	if err := validateFilter(prepared.Filter); err != nil {
		return CleanupJob{}, err
	}
	if prepared.All && prepared.Filter != (Filter{}) || !prepared.All && prepared.Filter == (Filter{}) {
		return CleanupJob{}, ErrInvalid
	}
	if err := s.authorizeWrite(ctx, tx, adminID); err != nil {
		return CleanupJob{}, err
	}
	var currentMax int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM management_operations").Scan(&currentMax); err != nil {
		return CleanupJob{}, err
	}
	if prepared.CutoffMaxID > currentMax {
		return CleanupJob{}, ErrInvalid
	}
	encoded, _ := json.Marshal(prepared.Filter)
	existing, err := readJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM management_operation_cleanup_jobs WHERE request_id=?", requestID))
	if err == nil {
		old, _ := json.Marshal(existing.Filter)
		if existing.Actor.ID != fmt.Sprint(adminID) || string(old) != string(encoded) || existing.CutoffMaxID != prepared.CutoffMaxID {
			return CleanupJob{}, ErrInvalid
		}
		MarkReplay(ctx)
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CleanupJob{}, err
	}
	subject, err := SubjectTx(ctx, tx, adminID)
	if err != nil {
		return CleanupJob{}, err
	}
	at := nowUnix()
	result, err := tx.ExecContext(ctx, `INSERT INTO management_operation_cleanup_jobs(request_id,created_at,updated_at,actor_id,actor_name,actor_role,filter_json,cutoff_max_id,state) VALUES(?,?,?,?,?,?,?,?,'pending')`, requestID, at, at, subject.ID, subject.Name, subject.Role, string(encoded), prepared.CutoffMaxID)
	if err != nil {
		return CleanupJob{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return CleanupJob{}, err
	}
	meta, has := MetadataFromContext(ctx)
	if !has {
		meta = Metadata{EventID: newEventID(), CreatedAt: at, Source: SourceWeb, ExecutorKind: "human", Actor: subject, Authorizer: subject}
	}
	meta.Domain = "management-operations"
	meta.Action = "cleanup_start"
	meta.CorrelationKind = "cleanup_job"
	meta.CorrelationID = fmt.Sprint(id)
	meta.Summary = fmt.Sprintf("清理截至记录 %d 的管理操作记录", prepared.CutoffMaxID)
	operationID, err := RecordTx(ctx, tx, Record{Metadata: meta, Result: Result{Outcome: Accepted, Code: "queued", Items: []Item{{Action: "cleanup_start", TargetKind: "cleanup_job", TargetID: fmt.Sprint(id), Outcome: Accepted}}}})
	if err != nil {
		return CleanupJob{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET start_operation_id=? WHERE id=?", operationID, id); err != nil {
		return CleanupJob{}, err
	}
	return readJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM management_operation_cleanup_jobs WHERE id=?", id))
}

const jobColumns = "id,created_at,updated_at,actor_id,actor_name,actor_role,filter_json,cutoff_max_id,state,current_operation_id,after_operation_id,deleted_operations,deleted_items,skipped_active,error_code"

func readJob(row scanner) (CleanupJob, error) {
	var v CleanupJob
	var filter string
	err := row.Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt, &v.Actor.ID, &v.Actor.Name, &v.Actor.Role, &filter, &v.CutoffMaxID, &v.State, &v.current, &v.after, &v.DeletedOperations, &v.DeletedItems, &v.SkippedActive, &v.ErrorCode)
	if err == nil {
		err = json.Unmarshal([]byte(filter), &v.Filter)
	}
	return v, err
}
func (s *Service) CleanupStatus(ctx context.Context, adminID, id int64) (CleanupJob, error) {
	tx, err := s.ReadDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CleanupJob{}, err
	}
	defer tx.Rollback()
	if err = requireAdmin(ctx, tx, adminID); err != nil {
		return CleanupJob{}, err
	}
	job, err := readJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM management_operation_cleanup_jobs WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return job, err
}
func (s *Service) ResumeCleanup(ctx context.Context, adminID, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.authorizeWrite(ctx, tx, adminID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET state='pending',error_code='',updated_at=? WHERE id=? AND state='failed'", nowUnix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalid
	}
	subject, err := SubjectTx(ctx, tx, adminID)
	if err != nil {
		return err
	}
	meta, ok := MetadataFromContext(ctx)
	if !ok {
		meta = Metadata{EventID: newEventID(), CreatedAt: nowUnix(), Source: SourceWeb, ExecutorKind: "human", Actor: subject, Authorizer: subject}
	}
	meta.Domain = "management-operations"
	meta.Action = "cleanup_resume"
	meta.Summary = "继续清理管理操作记录"
	meta.CorrelationKind = "cleanup_job"
	meta.CorrelationID = fmt.Sprint(id)
	operationID, err := RecordTx(ctx, tx, Record{Metadata: meta, Result: Result{Outcome: Accepted, Code: "queued", Items: []Item{{Action: "cleanup_resume", TargetKind: "cleanup_job", TargetID: fmt.Sprint(id), Outcome: Accepted}}}})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE management_operation_cleanup_jobs SET start_operation_id=? WHERE id=?", operationID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Run resumes pending work after restart. Each step yields the single writer;
// cancellation never marks a resumable job as failed.
func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		active, err := s.Step(ctx)
		delay := time.Second
		if active {
			delay = 25 * time.Millisecond
		}
		if err != nil {
			delay = 5 * time.Second
			if ctx.Err() == nil {
				slog.Error("management history cleanup failed", "code", "storage_error")
			}
		}
		timer.Reset(delay)
	}
}

func (s *Service) authorizeWrite(ctx context.Context, tx *sql.Tx, id int64) error {
	if err := requireAdmin(ctx, tx, id); err != nil {
		return err
	}
	if s.Authorizer != nil {
		return s.Authorizer.AuthorizeAdmin(ctx, tx, id)
	}
	return nil
}
