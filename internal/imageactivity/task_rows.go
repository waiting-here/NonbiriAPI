package imageactivity

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const taskColumns = "accepted_seq,id,user_id,model_id,model_revision,upstream_revision,control_id,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,created_at,updated_at,queue_deadline,execution_timeout_seconds,dispatched_at,execution_deadline,completed_at,upstream_task_id,next_poll_at,poll_count,http_seq,cleanup_deadline,actual_images,result_expires_at,error_code"

type taskRow struct {
	seq                                                                                int64
	id                                                                                 string
	user                                                                               int64
	modelID                                                                            string
	modelRevision, upstreamRevision                                                    int64
	controlID                                                                          string
	n                                                                                  int
	price                                                                              ledger.SketchPayment
	state, finance, slot                                                               string
	created, updated, queueDeadline                                                    int64
	executionSeconds                                                                   int
	dispatched, executionDeadline, completed, nextPoll, cleanupDeadline, resultExpires sql.NullInt64
	upstreamID                                                                         string
	pollCount, httpSeq, actualImages                                                   int
	errorCode                                                                          string
}
type rowScanner interface{ Scan(...any) error }

func scanTask(row rowScanner) (taskRow, error) {
	var r taskRow
	var user, modelRevision, upstreamRevision, n sql.NullInt64
	var modelID, controlID, upstreamID, errorCode sql.NullString
	var paper, brush []byte
	err := row.Scan(&r.seq, &r.id, &user, &modelID, &modelRevision, &upstreamRevision, &controlID, &n, &paper, &brush, &r.state, &r.finance, &r.slot, &r.created, &r.updated, &r.queueDeadline, &r.executionSeconds, &r.dispatched, &r.executionDeadline, &r.completed, &upstreamID, &r.nextPoll, &r.pollCount, &r.httpSeq, &r.cleanupDeadline, &r.actualImages, &r.resultExpires, &errorCode)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.user = user.Int64
	r.modelID = modelID.String
	r.modelRevision = modelRevision.Int64
	r.upstreamRevision = upstreamRevision.Int64
	r.controlID = controlID.String
	r.n = int(n.Int64)
	r.upstreamID = upstreamID.String
	r.errorCode = errorCode.String
	if r.user > 0 {
		r.price.Paper, err = decodeAmount(paper)
		if err != nil {
			return r, err
		}
		r.price.Brush, err = decodeAmount(brush)
		if err != nil {
			return r, err
		}
	}
	return r, nil
}
func taskTx(ctx context.Context, tx *sql.Tx, id string) (taskRow, error) {
	return scanTask(tx.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE id=?", id))
}
func (s *Service) taskViewTx(ctx context.Context, tx *sql.Tx, r taskRow, now int64) (Task, error) {
	if r.user <= 0 {
		return Task{}, ErrNotFound
	}
	out := Task{ID: r.id, ModelID: r.modelID, Status: r.state, N: r.n, ActualImages: r.actualImages, CreatedAt: r.created, DispatchedAt: nullTime(r.dispatched), CompletedAt: nullTime(r.completed), Charge: paymentPrice(r.price), Refund: zeroPrice(), ResultExpiresAt: nullTime(r.resultExpires), Images: []ImageInfo{}}
	switch r.finance {
	case "reserved":
		out.BillingState = "reserved"
	case "settled":
		out.BillingState = "charged"
	case "refunded":
		out.BillingState = "refunded"
		out.Charge = zeroPrice()
		out.Refund = paymentPrice(r.price)
	default:
		return out, ErrNotFound
	}
	if r.errorCode != "" {
		code := r.errorCode
		out.ErrorCode = &code
	}
	if r.state == "queued" {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM image_activity_tasks WHERE state='queued' AND accepted_seq<=?", r.seq).Scan(&n); err != nil {
			return out, err
		}
		out.QueuePosition = &n
	}
	if r.state == "succeeded" {
		out.Images = s.memory.metadata(r.id, r.user, now)
		out.ResultAvailable = len(out.Images) > 0
	}
	return out, nil
}
func (s *Service) GetTask(ctx context.Context, user int64, id string) (Task, error) {
	now, err := s.now()
	if err != nil {
		return Task{}, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return Task{}, err
	}
	row, err := taskTx(ctx, tx, id)
	if err != nil {
		return Task{}, err
	}
	if row.user != user || row.completed.Valid && row.completed.Int64 <= now-taskLifetime {
		return Task{}, ErrNotFound
	}
	out, err := s.taskViewTx(ctx, tx, row, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) ListTasks(ctx context.Context, user int64, limit int, cursor string) (Page[Task], error) {
	out := Page[Task]{Data: []Task{}}
	if !pageLimit(limit) {
		return out, ErrInvalid
	}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	sc := scope("tasks", user, "")
	after, err := s.readCursor(cursor, sc)
	if err != nil {
		return out, err
	}
	before := int64(9223372036854775807)
	if after != "" {
		before, err = decimalRevision(after, false)
		if err != nil {
			return out, err
		}
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE user_id=? AND accepted_seq<? AND (completed_at IS NULL OR completed_at>?) ORDER BY accepted_seq DESC LIMIT ?", user, before, now-taskLifetime, limit+1)
	if err != nil {
		return out, err
	}
	records := []taskRow{}
	for rows.Next() {
		r, e := scanTask(rows)
		if e != nil {
			_ = rows.Close()
			return out, e
		}
		records = append(records, r)
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(records) > limit {
		records = records[:limit]
		next, e := s.cursor(sc, strconv.FormatInt(records[len(records)-1].seq, 10))
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	for _, r := range records {
		v, e := s.taskViewTx(ctx, tx, r, now)
		if e != nil {
			return out, e
		}
		out.Data = append(out.Data, v)
	}
	return out, tx.Commit()
}
func (s *Service) GetQueue(ctx context.Context, user int64) (Queue, error) {
	out := Queue{Own: []OwnPosition{}}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM image_activity_tasks WHERE state='queued'").Scan(&out.Queued); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM image_activity_tasks WHERE finance_state='reserved' AND state IN ('dispatching','running')").Scan(&out.Running); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM image_upstream_control WHERE protection_paused=1)").Scan(&out.DispatchPaused); err != nil {
		return out, err
	}
	var maintenance, paused bool
	if err = tx.QueryRowContext(ctx, "SELECT enabled FROM maintenance_state WHERE id=1").Scan(&maintenance); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT paused FROM limited_activity_configs WHERE activity_key='picture-book'").Scan(&paused); err != nil {
		return out, err
	}
	out.DispatchPaused = out.DispatchPaused || maintenance || paused
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.created_at,CASE WHEN t.state='queued' THEN (SELECT count(*) FROM image_activity_tasks q WHERE q.state='queued' AND q.accepted_seq<=t.accepted_seq) ELSE NULL END FROM image_activity_tasks t WHERE t.user_id=? AND t.finance_state='reserved' ORDER BY t.accepted_seq LIMIT 100`, user)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item OwnPosition
		var pos sql.NullInt64
		if err = rows.Scan(&item.TaskID, &item.AcceptedAt, &pos); err != nil {
			return out, err
		}
		if pos.Valid {
			v := int(pos.Int64)
			item.Position = &v
		}
		out.Own = append(out.Own, item)
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
