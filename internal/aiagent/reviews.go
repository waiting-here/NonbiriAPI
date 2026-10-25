package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PendingSubmission is called only by a successful donor submission, inside
// its transaction and after deterministic auto-approval. Enabling the feature
// never scans historical pending donations, and there is no manual enqueue API.
func (s *Service) PendingSubmission(ctx context.Context, tx *sql.Tx, donationID int64) error {
	settings, _, _, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if settings.Mode == "off" {
		return nil
	}
	now := s.now().UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_review_events(donation_id,sequence,mode,created_at,updated_at) VALUES(?,1,?,?,?) ON CONFLICT(donation_id) DO UPDATE SET sequence=sequence+1,mode=excluded.mode,updated_at=excluded.updated_at`, donationID, settings.Mode, now, now)
	return err
}

func (s *Service) collectReviewEvents(ctx context.Context) error {
	rows, err := s.read.QueryContext(ctx, `SELECT id,donation_id,sequence,mode FROM ai_agent_review_events ORDER BY id LIMIT 32`)
	if err != nil {
		return err
	}
	type event struct {
		eventID, id, sequence int64
		mode                  string
	}
	events := []event{}
	for rows.Next() {
		var e event
		if err = rows.Scan(&e.eventID, &e.id, &e.sequence, &e.mode); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range events {
		intelligence, signature, readErr := s.config.Operations.Intelligence(ctx, e.id)
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return readErr
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		settings, _, owner, err := readSettings(ctx, tx)
		if err != nil {
			tx.Rollback()
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM ai_agent_review_events WHERE id=? AND sequence=?`, e.eventID, e.sequence)
		if err != nil {
			tx.Rollback()
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			tx.Rollback()
			return err
		}
		if count == 1 && settings.Mode != "off" && readErr == nil && signature != "" {
			now := s.now().UnixMilli()
			// Any older result is retained for review, but cannot approve the
			// newly submitted version. Running calls are stopped on dispatch or
			// final signature verification without holding this writer open.
			_, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='cancelled',error='捐赠已提交新版本',updated_at=? WHERE donation_id=? AND kind='auto_review' AND state IN ('pending','running')`, now, e.id)
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,intelligence,created_at,updated_at) VALUES('auto_review',?,'admin',?,?,?,?,'pending',?,?,?)`, owner.ID, owner.Label, e.id, signature, e.mode, string(intelligence), now, now)
			}
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) finishReview(ctx context.Context, job Job, actor Actor, outcome ReviewResult) error {
	if outcome.Decision != "approve" && outcome.Decision != "reject" && outcome.Decision != "manual" || outcome.InternalReason == "" || outcome.Decision == "approve" && len(outcome.Bindings) == 0 || outcome.Decision != "approve" && len(outcome.Bindings) > 0 {
		return ErrInvalid
	}
	var signature, mode string
	if err := s.read.QueryRowContext(ctx, `SELECT revision_signature,mode FROM ai_agent_jobs WHERE id=?`, job.ID).Scan(&signature, &mode); err != nil {
		return err
	}
	operations, validationErr := s.config.Operations.ReviewOperations(ctx, job.DonationID, signature, outcome)
	var pending *prepared
	if validationErr == nil && mode == "auto" && outcome.Decision != "manual" {
		pending, validationErr = s.prepare(ctx, actor, operations)
	}
	committed, applied := false, false
	defer func() {
		if pending != nil {
			pending.Finalize(committed && applied)
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state,mode FROM ai_agent_jobs WHERE id=?`, job.ID).Scan(&state, &mode); err != nil {
		return err
	}
	if state != "running" {
		return ErrConflict
	}
	settings, _, _, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	if settings.Mode == "off" {
		return ErrConflict
	}
	if err = s.authorize(ctx, tx, actor, "ai.auto"); err != nil {
		return err
	}
	execution := json.RawMessage(`{"status":"preview","applied":false}`)
	if validationErr != nil {
		execution, _ = json.Marshal(map[string]any{"status": "invalid", "applied": false, "reason": validationErr.Error()})
	} else if outcome.Decision == "manual" {
		execution = json.RawMessage(`{"status":"manual","applied":false}`)
	} else if mode == "auto" && settings.Mode == "auto" && pending != nil {
		execution, err = pending.Apply(ctx, tx)
		if err != nil {
			_ = tx.Rollback()
			pending.Finalize(false)
			pending = nil
			return s.saveReviewFailure(ctx, job, outcome, err)
		}
		applied = true
	}
	raw, _ := json.Marshal(outcome)
	if _, err = tx.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='completed',review_result=?,execution=?,updated_at=? WHERE id=? AND state='running'`, string(raw), string(execution), s.now().UnixMilli(), job.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Service) saveReviewFailure(ctx context.Context, job Job, outcome ReviewResult, cause error) error {
	raw, _ := json.Marshal(outcome)
	execution, _ := json.Marshal(map[string]any{"status": "failed", "applied": false, "reason": cause.Error()})
	_, err := s.db.ExecContext(ctx, `UPDATE ai_agent_jobs SET state='completed',review_result=?,execution=?,updated_at=? WHERE id=? AND state='running'`, string(raw), string(execution), s.now().UnixMilli(), job.ID)
	return err
}

func (s *Service) Reviews(ctx context.Context, actor Actor, page, size int) (Page[Review], error) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 100 || page > 1000000 {
		return Page[Review]{}, ErrInvalid
	}
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Page[Review]{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.review.read"); err != nil {
		return Page[Review]{}, err
	}
	out := Page[Review]{Items: []Review{}, Page: page, PageSize: size}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_agent_jobs WHERE kind='auto_review'`).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE kind='auto_review' ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, Review{Job: j, Intelligence: json.RawMessage(`{}`)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for i := range out.Items {
		if err = loadReviewDetails(ctx, tx, &out.Items[i], false); err != nil {
			return out, err
		}
	}
	if err = tx.Rollback(); err != nil {
		return out, err
	}
	for i := range out.Items {
		out.Items[i].Job.Queue, _ = s.config.Driver.Queue(ctx, out.Items[i].Job.RequestID)
	}
	return out, nil
}

func loadReviewDetails(ctx context.Context, tx *sql.Tx, r *Review, full bool) error {
	var result, execution sql.NullString
	var intel string
	err := tx.QueryRowContext(ctx, `SELECT review_result,execution,CASE WHEN ? THEN intelligence ELSE '{}' END FROM ai_agent_jobs WHERE id=? AND kind='auto_review'`, full, r.Job.ID).Scan(&result, &execution, &intel)
	if err != nil {
		return err
	}
	if result.Valid {
		var value ReviewResult
		if err = json.Unmarshal([]byte(result.String), &value); err != nil {
			return err
		}
		r.Result = &value
	}
	if execution.Valid {
		r.Execution = json.RawMessage(execution.String)
	}
	r.Intelligence = json.RawMessage(intel)
	if full {
		r.Turns, err = modelResults(ctx, tx, r.Job.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Review(ctx context.Context, actor Actor, id int64) (Review, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Review{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, actor, "ai.review.read"); err != nil {
		return Review{}, err
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ai_agent_jobs WHERE id=? AND kind='auto_review'`, id))
	if err != nil {
		return Review{}, err
	}
	r := Review{Job: j}
	err = loadReviewDetails(ctx, tx, &r, true)
	if err != nil {
		return r, err
	}
	if err = tx.Rollback(); err != nil {
		return r, err
	}
	r.Job.Queue, _ = s.config.Driver.Queue(ctx, r.Job.RequestID)
	return r, nil
}
