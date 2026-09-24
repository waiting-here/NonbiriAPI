package imageactivity

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

// Run starts only after the host finishes transactional startup recovery.
func (s *Service) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) || s.stopped.Load() {
		return ErrConflict
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer s.Close()
	for {
		if err := s.Step(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && s.config.ReportError != nil {
				s.config.ReportError(err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.background.Done():
			return nil
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.stepMu.Lock()
	s.jobsMu.Lock()
	if s.stopped.CompareAndSwap(false, true) {
		s.cancel()
	}
	s.jobsMu.Unlock()
	s.stepMu.Unlock()
	s.workers.Wait()
	s.memory.close()
	return nil
}
func (s *Service) launch(id string, job func()) {
	s.jobsMu.Lock()
	if s.jobs[id] || s.stopped.Load() {
		s.jobsMu.Unlock()
		return
	}
	s.jobs[id] = true
	s.workers.Add(1)
	s.jobsMu.Unlock()
	go func() {
		defer s.workers.Done()
		defer func() { s.jobsMu.Lock(); delete(s.jobs, id); s.jobsMu.Unlock(); s.signal() }()
		job()
	}()
}
func (s *Service) hasJob(id string) bool { s.jobsMu.Lock(); defer s.jobsMu.Unlock(); return s.jobs[id] }
func (s *Service) Step(ctx context.Context) error {
	if s.stopped.Load() {
		return ErrUnavailable
	}
	s.stepMu.Lock()
	defer s.stepMu.Unlock()
	if s.stopped.Load() {
		return ErrUnavailable
	}
	now, err := s.now()
	if err != nil {
		return err
	}
	s.memory.expire(now)
	if err = s.expireQueued(ctx, now); err != nil {
		return err
	}
	// Active polls run before admissions; no lock is held while waiting for SQL.
	rows, err := s.config.Database.QueryContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE upstream_revision IS NOT NULL AND state<>'queued' AND (state IN ('dispatching','running') OR (state='unknown_refunded' AND cleanup_deadline>?)) ORDER BY COALESCE(next_poll_at,execution_deadline),accepted_seq LIMIT 100", now)
	if err != nil {
		return err
	}
	active := []taskRow{}
	for rows.Next() {
		r, e := scanTask(rows)
		if e != nil {
			rows.Close()
			return e
		}
		active = append(active, r)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, r := range active {
		if s.hasJob(r.id) {
			continue
		}
		snapshot, e := s.readSnapshot(ctx, r.upstreamRevision)
		if e != nil {
			return e
		}
		budget, e := s.memoryBudget(ctx, snapshot.memoryMiB)
		if e != nil {
			return e
		}
		if !s.memory.restoreExecution(r.id, r.user, budget) {
			continue
		}
		id := r.id
		s.launch(id, func() { s.runTask(id, false) })
	}
	if err = s.stepRefresh(ctx, now); err != nil {
		return err
	}
	if err = s.dispatchHead(ctx, now); err != nil {
		return err
	}
	return s.reconcileMemory(ctx, now)
}

// Queue deadlines belong to each accepted task. A protected FIFO head must not
// prevent later tasks with shorter configuration snapshots from being refunded.
func (s *Service) expireQueued(ctx context.Context, now int64) error {
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := s.cancelQueuedTx(ctx, tx, "id IN (SELECT id FROM image_activity_tasks WHERE state='queued' AND queue_deadline<=? ORDER BY queue_deadline,accepted_seq LIMIT 100)", []any{now}, "queue_timeout", now)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for _, id := range ids {
		s.memory.purge(id)
	}
	return nil
}
func (s *Service) readSnapshot(ctx context.Context, revision int64) (upstreamSnapshot, error) {
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return upstreamSnapshot{}, err
	}
	defer tx.Rollback()
	snapshot, err := upstreamTx(ctx, tx, revision)
	if err != nil {
		return snapshot, err
	}
	return snapshot, tx.Commit()
}
func (s *Service) memoryBudget(ctx context.Context, fallback int) (int64, error) {
	var mib int
	err := s.config.Database.QueryRowContext(ctx, "SELECT r.memory_budget_mib FROM image_activity_state s JOIN image_upstream_revisions r ON r.revision=s.upstream_revision WHERE s.id=1").Scan(&mib)
	if errors.Is(err, sql.ErrNoRows) {
		mib = fallback
		err = nil
	}
	return int64(mib) << 20, err
}
func pausedTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var paused bool
	err := tx.QueryRowContext(ctx, "SELECT (SELECT enabled FROM maintenance_state WHERE id=1) OR (SELECT paused FROM limited_activity_configs WHERE activity_key='picture-book')").Scan(&paused)
	return paused, err
}
func (s *Service) dispatchHead(ctx context.Context, now int64) error {
	// Read the FIFO head, reserve RAM, then repeat every predicate in the writer.
	row, err := scanTask(s.config.Database.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE state='queued' ORDER BY accepted_seq LIMIT 1"))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	snapshot, err := s.readSnapshot(ctx, row.upstreamRevision)
	if err != nil {
		return err
	}
	budget, err := s.memoryBudget(ctx, snapshot.memoryMiB)
	if err != nil {
		return err
	}
	reserved := s.memory.prepareExecution(row.id, budget)
	defer func() {
		if reserved {
			s.memory.rollbackExecution(row.id)
		}
	}()
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := taskTx(ctx, tx, row.id)
	if err != nil {
		return err
	}
	if current.state != "queued" {
		return nil
	}
	var first string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM image_activity_tasks WHERE state='queued' ORDER BY accepted_seq LIMIT 1").Scan(&first); err != nil {
		return err
	}
	if first != row.id {
		return nil
	}
	paused, err := pausedTx(ctx, tx)
	if err != nil {
		return err
	}
	code := ""
	if paused {
		code = "activity_paused"
	} else if now >= current.queueDeadline {
		code = "queue_timeout"
	} else if err = eligibleUserTx(ctx, tx, current.user, now); err != nil {
		if !errors.Is(err, authz.ErrUnauthorized) && !errors.Is(err, authz.ErrForbidden) {
			return err
		}
		code = "account_restricted"
	}
	var enabled bool
	if code == "" {
		err = tx.QueryRowContext(ctx, "SELECT r.enabled FROM image_activity_models m JOIN image_model_revisions r ON r.model_id=m.id AND r.revision=m.current_revision WHERE m.id=?", current.modelID).Scan(&enabled)
		if err != nil {
			return err
		}
		if !enabled {
			code = "model_unavailable"
		}
	}
	if code != "" {
		if _, err = s.finishFinancialTx(ctx, tx, current, "cancelled", code, 0, now, false); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		s.memory.purge(row.id)
		return nil
	}
	if !reserved {
		return nil
	}
	control, err := controlTx(ctx, tx, current.controlID)
	if err != nil {
		return err
	}
	if control.Paused {
		return nil
	}
	occupied, err := occupiedTx(ctx, tx, current.controlID)
	if err != nil {
		return err
	}
	var concurrency int
	if err = tx.QueryRowContext(ctx, "SELECT concurrency_limit FROM image_upstream_control WHERE id=?", current.controlID).Scan(&concurrency); err != nil {
		return err
	}
	if occupied >= concurrency {
		return nil
	}
	var due bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM image_activity_tasks WHERE control_id=? AND upstream_task_id IS NOT NULL AND next_poll_at<=? AND state IN ('running','unknown_refunded'))", current.controlID, now).Scan(&due); err != nil {
		return err
	}
	if due {
		return nil
	}
	permit, _, err := reserveHTTPTx(ctx, tx, current.controlID, s.config.Now().UnixMilli())
	if err != nil {
		return err
	}
	if !permit {
		return nil
	}
	err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_tasks SET state='dispatching',slot_state='held',dispatched_at=?,execution_deadline=?,http_seq=http_seq+1,updated_at=? WHERE id=? AND state='queued'", now, now+int64(current.executionSeconds), now, row.id))
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// The deferred rollback must not shrink an active transport reservation.
	reserved = false
	s.launch(row.id, func() { s.runTask(row.id, true) })
	return nil
}
func (s *Service) readTask(ctx context.Context, id string) (taskRow, error) {
	return scanTask(s.config.Database.QueryRowContext(ctx, "SELECT "+taskColumns+" FROM image_activity_tasks WHERE id=?", id))
}
func (s *Service) runTask(id string, submit bool) {
	published := false
	defer func() {
		if !published {
			s.memory.discard(id)
		}
	}()
	ctx := s.background
	row, err := s.readTask(ctx, id)
	if err != nil {
		return
	}
	snapshot, err := s.readSnapshot(ctx, row.upstreamRevision)
	if err != nil {
		return
	}
	if submit {
		payload := s.memory.payload(id)
		// The committed dispatch fence is never reset, even if shutdown prevents I/O.
		if len(payload) == 0 {
			s.markUnknown(ctx, id)
			return
		}
		now, e := s.now()
		if e != nil {
			return
		}
		bound, cancel := context.WithTimeout(ctx, time.Duration(row.executionDeadline.Int64-now)*time.Second)
		target, e := requestURL(snapshot.baseURL, snapshot.adapter.Submit.Path, "")
		var response responseData
		if e != nil {
			response.err = e
		} else {
			response = s.perform(bound, snapshot, http.MethodPost, target, payload, egress.ImageJSON, observability.DiagnosticRef{TaskID: id, AttemptSeq: row.httpSeq}, true)
		}
		cancel()
		s.memory.dropPayload(id)
		if ctx.Err() != nil {
			return
		}
		latest, readErr := s.readTask(ctx, id)
		if readErr == nil && latest.finance == "deleted" {
			if state, e := submissionState(response.body, snapshot.adapter); response.err == nil && e == nil && (state == "succeeded" || state == "failed") {
				s.finishCleanup(ctx, id, true)
				return
			}
		}
		if response.status >= 300 {
			published = s.settle(ctx, id, "failed", "upstream_failed", nil)
			return
		}
		if response.err != nil {
			if !s.markUnknown(ctx, id) {
				return
			}
		} else {
			state, e := submissionState(response.body, snapshot.adapter)
			if e != nil {
				s.captureImageOmission(ctx, observability.DiagnosticRef{TaskID: id, AttemptSeq: response.seq}, response, "invalid_response")
			}
			if e == nil && state == "failed" {
				s.captureTaskFailure(ctx, row, response)
				published = s.settle(ctx, id, "failed", "upstream_failed", nil)
				return
			}
			if e == nil && state == "succeeded" {
				imgs := s.extractImages(ctx, row, snapshot, response.body, row.executionDeadline.Int64)
				if len(imgs) > 0 {
					published = s.settle(ctx, id, "succeeded", "", imgs)
				} else {
					s.captureImageOmission(ctx, observability.DiagnosticRef{TaskID: id, AttemptSeq: response.seq}, response, "invalid_images")
					published = s.settle(ctx, id, "failed", "invalid_result", nil)
				}
				return
			}
			upstreamID := ""
			if state == "running" && snapshot.adapter.Response.TaskIDPointer != nil {
				upstreamID, e = rawString(response.body, *snapshot.adapter.Response.TaskIDPointer, 2048)
			}
			if e != nil || upstreamID == "" || snapshot.adapter.Poll == nil {
				s.captureImageOmission(ctx, observability.DiagnosticRef{TaskID: id, AttemptSeq: response.seq}, response, "missing_task_receipt")
				if !s.markUnknown(ctx, id) {
					return
				}
			} else if !s.acceptUpstreamID(ctx, id, upstreamID) {
				return
			}
		}
	}
	for ctx.Err() == nil {
		row, err = s.readTask(ctx, id)
		if err != nil {
			return
		}
		if row.upstreamRevision == 0 {
			return
		}
		now, e := s.now()
		if e != nil {
			return
		}
		deadline := row.executionDeadline.Int64
		cleanup := row.finance != "reserved"
		if cleanup {
			if !row.cleanupDeadline.Valid || now >= row.cleanupDeadline.Int64 {
				s.finishCleanup(ctx, id, false)
				return
			}
			deadline = row.cleanupDeadline.Int64
		} else if now >= deadline {
			if !s.unknownRefund(ctx, id) {
				return
			}
			continue
		}
		if row.upstreamID == "" {
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		if row.nextPoll.Valid && row.nextPoll.Int64 > now {
			delay := time.Duration(row.nextPoll.Int64-now) * time.Second
			if delay > time.Second {
				delay = time.Second
			}
			if !waitContext(ctx, delay) {
				return
			}
			continue
		}
		target, e := requestURL(snapshot.baseURL, snapshot.adapter.Poll.Path, row.upstreamID)
		if e != nil {
			return
		}
		response := s.taskGET(ctx, row, snapshot, target, egress.ImageJSON, true, deadline)
		if ctx.Err() != nil {
			return
		}
		state, parseErr := responseState(response.body, snapshot.adapter.Response)
		if response.err == nil && parseErr != nil {
			s.captureImageOmission(ctx, observability.DiagnosticRef{TaskID: id, AttemptSeq: response.seq}, response, "invalid_response")
		}
		if response.err == nil && parseErr == nil && (state == "succeeded" || state == "failed") {
			if cleanup {
				s.finishCleanup(ctx, id, true)
				return
			}
			if state == "failed" {
				s.captureTaskFailure(ctx, row, response)
				published = s.settle(ctx, id, "failed", "upstream_failed", nil)
				return
			}
			imgs := s.extractImages(ctx, row, snapshot, response.body, deadline)
			if len(imgs) > 0 {
				published = s.settle(ctx, id, "succeeded", "", imgs)
			} else {
				s.captureImageOmission(ctx, observability.DiagnosticRef{TaskID: id, AttemptSeq: response.seq}, response, "invalid_images")
				published = s.settle(ctx, id, "failed", "invalid_result", nil)
			}
			return
		}
		if err = s.schedulePoll(ctx, id, response.header); err != nil {
			return
		}
	}
}
func (s *Service) captureTaskFailure(ctx context.Context, row taskRow, response responseData) {
	scoped := s.config.Diagnostics.ErrorScope(ctx, observability.DiagnosticRef{TaskID: row.id, AttemptSeq: response.seq})
	upstreamerror.CaptureEvent(scoped, response.status, response.header.Get("Content-Type"), response.body)
}
func (s *Service) acceptUpstreamID(ctx context.Context, id, upstreamID string) bool {
	for ctx.Err() == nil {
		now, err := s.now()
		if err != nil {
			return false
		}
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			if !waitContext(ctx, 100*time.Millisecond) {
				return false
			}
			continue
		}
		r, err := taskTx(ctx, tx, id)
		if err == nil && r.upstreamRevision != 0 {
			err = requireOne(tx.ExecContext(ctx, "UPDATE image_activity_tasks SET state='running',upstream_task_id=?,next_poll_at=?,updated_at=? WHERE id=? AND state='dispatching'", upstreamID, now+4, now, id))
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err == nil {
			return true
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
			return false
		}
		if !waitContext(ctx, 100*time.Millisecond) {
			return false
		}
	}
	return false
}
func (s *Service) markUnknown(ctx context.Context, id string) bool {
	for ctx.Err() == nil {
		now, err := s.now()
		if err != nil {
			return false
		}
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			if !waitContext(ctx, 100*time.Millisecond) {
				return false
			}
			continue
		}
		row, err := taskTx(ctx, tx, id)
		if err == nil {
			err = pauseControlTx(ctx, tx, row.controlID, "receipt_unknown", now)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET slot_state='uncertain',error_code='result_unknown',updated_at=? WHERE id=? AND slot_state='held'", now, id)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err == nil {
			return true
		}
		if !waitContext(ctx, 100*time.Millisecond) {
			return false
		}
	}
	return false
}
func (s *Service) unknownRefund(ctx context.Context, id string) bool {
	for ctx.Err() == nil {
		now, err := s.now()
		if err != nil {
			return false
		}
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			if !waitContext(ctx, 100*time.Millisecond) {
				return false
			}
			continue
		}
		row, err := taskTx(ctx, tx, id)
		if err == nil && row.finance == "reserved" {
			if row.slot != "ignored" {
				err = pauseControlTx(ctx, tx, row.controlID, "execution_timeout", now)
			}
			if err == nil {
				_, err = s.finishFinancialTx(ctx, tx, row, "unknown_refunded", "result_unknown", 0, now, false)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET cleanup_deadline=?,next_poll_at=? WHERE id=?", row.executionDeadline.Int64+300, now, id)
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err == nil {
			return true
		}
		if !waitContext(ctx, 100*time.Millisecond) {
			return false
		}
	}
	return false
}
func (s *Service) schedulePoll(ctx context.Context, id string, header http.Header) error {
	now, err := s.now()
	if err != nil {
		return err
	}
	row, err := s.readTask(ctx, id)
	if err != nil {
		return err
	}
	shift := row.pollCount
	if shift > 4 {
		shift = 4
	}
	delay := int64(4 << shift)
	if delay > 60 {
		delay = 60
	}
	hash := binary.BigEndian.Uint16([]byte(id[len(id)-2:]))
	delay += int64(hash % 3)
	if delay > 60 {
		delay = 60
	}
	if retry := retrySeconds(header, now); retry > delay {
		delay = retry
	}
	_, err = s.config.Database.ExecContext(ctx, "UPDATE image_activity_tasks SET next_poll_at=?,poll_count=MIN(poll_count+1,2147483647),updated_at=? WHERE id=? AND upstream_revision IS NOT NULL", now+delay, now, id)
	return err
}
func (s *Service) settle(ctx context.Context, id, state, code string, images []imageBytes) bool {
	for ctx.Err() == nil {
		now, err := s.now()
		if err != nil {
			return false
		}
		tx, err := s.config.Database.BeginTx(ctx, nil)
		if err != nil {
			if !waitContext(ctx, 100*time.Millisecond) {
				return false
			}
			continue
		}
		row, err := taskTx(ctx, tx, id)
		allowed := false
		won := false
		if err == nil && row.finance == "deleted" {
			tx.Rollback()
			s.finishCleanup(ctx, id, true)
			return false
		}
		if err == nil && row.finance == "reserved" {
			won, err = s.finishFinancialTx(ctx, tx, row, state, code, len(images), now, false)
			if err == nil && state == "succeeded" {
				eligibility := eligibleUserTx(ctx, tx, row.user, now)
				allowed = eligibility == nil
				if eligibility != nil && !errors.Is(eligibility, authz.ErrForbidden) && !errors.Is(eligibility, authz.ErrUnauthorized) {
					err = eligibility
				}
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err == nil {
			if won && allowed && len(images) > 0 {
				return s.memory.publish(id, row.user, images, now+resultLifetime)
			}
			return false
		}
		if errors.Is(err, ErrNotFound) {
			return false
		}
		if !waitContext(ctx, 100*time.Millisecond) {
			return false
		}
	}
	return false
}
func (s *Service) finishCleanup(ctx context.Context, id string, confirmed bool) {
	now, err := s.now()
	if err != nil {
		return
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	row, err := taskTx(ctx, tx, id)
	if err != nil {
		return
	}
	// A confirmed late completion frees the physical slot, but never changes
	// refund outcome or clears the administrator protection latch.
	slot := row.slot
	if confirmed {
		slot = "none"
	}
	if !confirmed && row.finance == "deleted" && slot == "held" {
		if err = pauseControlTx(ctx, tx, row.controlID, "receipt_unknown", now); err != nil {
			return
		}
		slot = "uncertain"
	}
	state := row.state
	if row.finance == "deleted" {
		state = "failed"
	}
	control := any(row.controlID)
	if slot == "none" {
		control = nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET state=?,slot_state=?,control_id=?,upstream_revision=NULL,upstream_task_id=NULL,next_poll_at=NULL,cleanup_deadline=NULL,completed_at=COALESCE(completed_at,?),updated_at=? WHERE id=?", state, slot, control, now, now, id)
	if err == nil {
		_ = tx.Commit()
	}
}
func (s *Service) reconcileMemory(ctx context.Context, now int64) error {
	for _, id := range s.memory.ids() {
		if s.hasJob(id) {
			continue
		}
		row, err := s.readTask(ctx, id)
		if errors.Is(err, ErrNotFound) {
			s.memory.purge(id)
			continue
		}
		if err != nil {
			return err
		}
		if row.user == 0 || row.state != "queued" && row.state != "succeeded" || row.resultExpires.Valid && row.resultExpires.Int64 <= now {
			s.memory.purge(id)
		}
	}
	return nil
}
