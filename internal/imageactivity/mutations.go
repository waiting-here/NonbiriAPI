package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

const (
	userPrefix  = "/api/limited-activities/picture-book"
	adminPrefix = "/admin/api/limited-activities/picture-book"
)

type submission struct {
	upstream upstreamSnapshot
	model    modelSnapshot
	payload  []byte
	n        int
	price    ledger.SketchPayment
}

func normalizeSubmitText(input SubmitInput) SubmitInput {
	input.Prompt = strings.ReplaceAll(input.Prompt, "\r\n", "\n")
	if len(input.NegativePrompt) > 0 {
		var text string
		if json.Unmarshal(input.NegativePrompt, &text) == nil {
			input.NegativePrompt, _ = json.Marshal(strings.ReplaceAll(text, "\r\n", "\n"))
		}
	}
	return input
}
func (s *Service) prepareSubmission(ctx context.Context, user int64, key string, input SubmitInput, now int64) (submission, *TaskResult, error) {
	var prepared submission
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return prepared, nil, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return prepared, nil, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return prepared, nil, err
	}
	d, err := s.beginReplay(ctx, tx, "user", user, key, http.MethodPost, userPrefix+"/tasks", input, now)
	if err != nil {
		return prepared, nil, err
	}
	if d.Kind == idempotency.Replay {
		var result TaskResult
		if json.Unmarshal(d.ResponseBody, &result) != nil {
			return prepared, nil, ErrInvariant
		}
		return prepared, &result, tx.Commit()
	}
	if _, err = s.config.Admission(ctx, tx, user, limitedactivities.PictureBook, now); err != nil {
		return prepared, nil, err
	}
	prepared.upstream, err = currentUpstreamTx(ctx, tx)
	if err != nil {
		return prepared, nil, err
	}
	prepared.model, err = modelTx(ctx, tx, input.ModelID, 0)
	if err != nil {
		return prepared, nil, err
	}
	expected, e := decimalRevision(input.ExpectedModelRevision, false)
	if e != nil {
		return prepared, nil, e
	}
	if prepared.model.revision != expected || prepared.model.controlID != prepared.upstream.controlID {
		return prepared, nil, ErrConflict
	}
	if !prepared.model.input.Enabled {
		return prepared, nil, ErrUnavailable
	}
	params, n, err := normalizeSubmit(input, prepared.model.input.Parameters, prepared.model.input.Combinations)
	if err != nil {
		return prepared, nil, err
	}
	prepared.n = n
	prepared.price, err = parsePayment(prepared.model.input.Price, n)
	if err != nil {
		return prepared, nil, err
	}
	mapping := prepared.model.input.Mapping
	if mapping.ModelPointer == "" {
		mapping = prepared.upstream.adapter.Submit.Mapping
	}
	prepared.payload, err = buildRequest(prepared.model.upstreamID, params, mapping)
	if err != nil {
		return prepared, nil, err
	}
	// A fresh idempotency row from this preparation is deliberately rolled back.
	// The real acceptance repeats it after acquiring its bounded memory ticket.
	return prepared, nil, tx.Rollback()
}
func (s *Service) Submit(ctx context.Context, user int64, key string, input SubmitInput) (MutationResult[TaskResult], error) {
	var out MutationResult[TaskResult]
	if s.stopped.Load() {
		return out, ErrUnavailable
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		return out, ErrInvalid
	}
	input = normalizeSubmitText(input)
	now, err := s.now()
	if err != nil {
		return out, err
	}
	prepared, replay, err := s.prepareSubmission(ctx, user, key, input, now)
	if err != nil {
		return out, err
	}
	if replay != nil {
		out.Value = *replay
		out.Replayed = true
		return out, nil
	}
	id, err := newID("img_")
	if err != nil {
		return out, err
	}
	if !s.memory.reserveQueued(id, user, prepared.payload, int64(prepared.upstream.memoryMiB)<<20) {
		return out, ErrCapacity
	}
	committed := false
	defer func() {
		if !committed {
			s.memory.discard(id)
		}
	}()
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return out, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return out, err
	}
	d, err := s.beginReplay(ctx, tx, "user", user, key, http.MethodPost, userPrefix+"/tasks", input, now)
	if err != nil {
		return out, err
	}
	if d.Kind == idempotency.Replay {
		if json.Unmarshal(d.ResponseBody, &out.Value) != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	if _, err = s.config.Admission(ctx, tx, user, limitedactivities.PictureBook, now); err != nil {
		return out, err
	}
	current, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	if current.stateRevision != prepared.upstream.stateRevision {
		return out, ErrConflict
	}
	model, err := modelTx(ctx, tx, input.ModelID, 0)
	if err != nil {
		return out, err
	}
	if model.revision != prepared.model.revision || !model.input.Enabled {
		return out, ErrConflict
	}
	var all, mine, rows int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(user_id=?),0) FROM image_activity_tasks WHERE finance_state='reserved'`, user).Scan(&all, &mine); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT task_rows FROM image_activity_state WHERE id=1").Scan(&rows); err != nil {
		return out, err
	}
	if all >= current.globalLimit || mine >= current.userLimit || rows >= 1000000 {
		return out, ErrCapacity
	}
	wallets, err := ledger.CreateSketchAccounts(ctx, tx, user, now)
	if err != nil {
		return out, err
	}
	escrow, err := codedSketch(ctx, tx, "image_activity_reserve")
	if err != nil {
		return out, err
	}
	operation, err := newID("op_")
	if err != nil {
		return out, err
	}
	ref, err := ledger.ImageTaskReservation(id)
	if err != nil {
		return out, err
	}
	one, _ := db.ParseU128Decimal("1")
	err = ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO image_activity_tasks(id,user_id,model_id,model_revision,upstream_revision,control_id,n,paper_charge_mag,brush_charge_mag,state,finance_state,slot_state,ledger_rows_remaining,created_at,updated_at,queue_deadline,execution_timeout_seconds) VALUES(?,?,?,?,?,?,?,?,?,'queued','reserved','none',?,?,?,?,?)`, id, user, model.id, model.revision, current.revision, current.controlID, prepared.n, encodeAmount(prepared.price.Paper), encodeAmount(prepared.price.Brush), db.EncodeU128(one), now, now, now+int64(current.queueSeconds), current.executionSeconds)
		return err
	})
	if err != nil {
		return out, err
	}
	plan, err := ledger.NewImageReserve(ledger.Meta{OperationID: operation, ActorUserID: user, CreatedAt: now}, id, wallets, escrow, prepared.price)
	if err != nil {
		return out, err
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		return out, err
	}
	if err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_tasks SET reserve_operation_id=? WHERE id=?", operation, id)); err != nil {
		return out, err
	}
	if err = s.config.Sources.RecordImageSourceTx(ctx, tx, id, user, now); err != nil {
		return out, err
	}
	if !missing(s.config.Activity) {
		if err = s.config.Activity.RecordLimitedActivityTx(ctx, tx, user, now); err != nil {
			return out, err
		}
	}
	row, err := taskTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Value.Task, err = s.taskViewTx(ctx, tx, row, now)
	if err != nil {
		return out, err
	}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	committed = true
	s.memory.commitQueued(id)
	s.signal()
	return out, nil
}
func (s *Service) Cancel(ctx context.Context, user int64, id, key string) (MutationResult[TaskResult], error) {
	var out MutationResult[TaskResult]
	if !db.ValidateOpaqueID(id, "img_") {
		return out, ErrInvalid
	}
	now, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, user, now); err != nil {
		return out, err
	}
	d, err := s.beginReplay(ctx, tx, "user", user, key, http.MethodPost, userPrefix+"/tasks/"+id+"/cancel", struct{}{}, now)
	if err != nil {
		return out, err
	}
	if d.Kind == idempotency.Replay {
		if json.Unmarshal(d.ResponseBody, &out.Value) != nil {
			return out, ErrInvariant
		}
		out.Replayed = true
		return out, tx.Commit()
	}
	row, err := taskTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if row.user != user {
		return out, ErrNotFound
	}
	if row.state != "queued" {
		return out, ErrConflict
	}
	if _, err = s.finishFinancialTx(ctx, tx, row, "cancelled", "cancelled_by_user", 0, now, false); err != nil {
		return out, err
	}
	if !missing(s.config.Activity) {
		if err = s.config.Activity.RecordLimitedActivityTx(ctx, tx, user, now); err != nil {
			return out, err
		}
	}
	row, err = taskTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Value.Task, err = s.taskViewTx(ctx, tx, row, now)
	if err != nil {
		return out, err
	}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	s.memory.purge(id)
	s.signal()
	return out, nil
}
