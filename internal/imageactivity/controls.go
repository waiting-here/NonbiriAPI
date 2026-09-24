package imageactivity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

// reserveHTTPTx consumes one shared physical-identity request permit. A limit
// reduction preserves prior timestamps rather than resetting the window.
func reserveHTTPTx(ctx context.Context, tx *sql.Tx, control string, nowMS int64) (bool, int64, error) {
	var raw []byte
	var limit int
	if err := tx.QueryRowContext(ctx, "SELECT http_times,rpm_limit FROM image_upstream_control WHERE id=?", control).Scan(&raw, &limit); err != nil {
		return false, 0, err
	}
	if len(raw)%8 != 0 || len(raw) > 80000 {
		return false, 0, ErrInvariant
	}
	live := make([]byte, 0, len(raw)+8)
	oldest := int64(math.MaxInt64)
	for i := 0; i < len(raw); i += 8 {
		t := int64(binary.BigEndian.Uint64(raw[i : i+8]))
		if t < 0 {
			return false, 0, ErrInvariant
		}
		if t > nowMS-60000 {
			live = append(live, raw[i:i+8]...)
			if t < oldest {
				oldest = t
			}
		}
	}
	if len(live)/8 >= limit {
		return false, oldest + 60000, nil
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(nowMS))
	live = append(live, encoded[:]...)
	_, err := tx.ExecContext(ctx, "UPDATE image_upstream_control SET http_times=?,updated_at=? WHERE id=?", live, nowMS/1000, control)
	return err == nil, 0, err
}

// pruneHTTPWindowTx revisits idle controls without waiting for another request.
// Live entries survive a backwards clock adjustment; a scan advances only the
// housekeeping timestamp and never resets limits or administrative protection.
func pruneHTTPWindowTx(ctx context.Context, tx *sql.Tx, now int64) (bool, error) {
	var id string
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT id,http_times FROM image_upstream_control WHERE length(http_times)>0 AND updated_at<=? ORDER BY updated_at,id LIMIT 1", now-60).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(raw)%8 != 0 || len(raw) > 80000 {
		return false, ErrInvariant
	}
	live := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i += 8 {
		at := int64(binary.BigEndian.Uint64(raw[i : i+8]))
		if at < 0 {
			return false, ErrInvariant
		}
		if at > now*1000-60000 {
			live = append(live, raw[i:i+8]...)
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE image_upstream_control SET http_times=?,updated_at=? WHERE id=?", live, now, id)
	return err == nil, err
}
func controlTx(ctx context.Context, tx *sql.Tx, id string) (Control, error) {
	var out Control
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT c.id,c.protection_paused,c.protection_reason,c.protection_revision,(SELECT count(*) FROM image_activity_tasks t WHERE t.control_id=c.id AND t.slot_state='uncertain') FROM image_upstream_control c WHERE c.id=?`, id).Scan(&out.ID, &out.Paused, &out.Reason, &revision, &out.UncertainSlots)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	out.Revision = strconv.FormatInt(revision, 10)
	return out, err
}
func pauseControlTx(ctx context.Context, tx *sql.Tx, id, reason string, now int64) error {
	return requireOne(tx.ExecContext(ctx, `UPDATE image_upstream_control SET protection_paused=1,protection_reason=CASE WHEN protection_paused=0 THEN ? ELSE protection_reason END,protection_revision=CASE WHEN protection_paused=0 AND protection_revision<9223372036854775807 THEN protection_revision+1 ELSE protection_revision END,updated_at=? WHERE id=?`, reason, now, id))
}
func occupiedTx(ctx context.Context, tx *sql.Tx, id string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM image_activity_tasks WHERE control_id=? AND slot_state IN ('held','uncertain'))+(SELECT count(*) FROM image_model_refreshes WHERE control_id=? AND state='running')`, id, id).Scan(&count)
	return count, err
}
func (s *Service) Controls(ctx context.Context, admin int64, limit int, cursor string) (Page[ControlRow], error) {
	out := Page[ControlRow]{Data: []ControlRow{}}
	if !pageLimit(limit) {
		return out, ErrInvalid
	}
	sc := scope("controls", admin, "")
	after, err := s.readCursor(cursor, sc)
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.protection_paused,c.protection_reason,c.protection_revision,(SELECT count(*) FROM image_activity_tasks t WHERE t.control_id=c.id AND t.slot_state='uncertain'),EXISTS(SELECT 1 FROM image_activity_state s JOIN image_upstream_revisions r ON r.revision=s.upstream_revision WHERE r.control_id=c.id),(SELECT count(*) FROM image_activity_tasks t WHERE t.control_id=c.id AND t.state='queued'),(SELECT count(*) FROM image_activity_tasks t WHERE t.control_id=c.id AND t.slot_state='held') FROM image_upstream_control c WHERE c.id>? ORDER BY c.id LIMIT ?`, after, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row ControlRow
		var revision int64
		if err = rows.Scan(&row.ID, &row.Paused, &row.Reason, &revision, &row.UncertainSlots, &row.Current, &row.Queued, &row.Running); err != nil {
			rows.Close()
			return out, err
		}
		row.Revision = strconv.FormatInt(revision, 10)
		out.Data = append(out.Data, row)
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Data) > limit {
		out.Data = out.Data[:limit]
		next, e := s.cursor(sc, out.Data[limit-1].ID)
		if e != nil {
			return out, e
		}
		out.NextCursor = &next
	}
	return out, tx.Commit()
}
func acceptedTx(ctx context.Context, tx *sql.Tx, id, kind string, admin, now int64, payload any, complete bool) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	state := "accepted"
	var terminal any
	if complete {
		state = "completed"
		terminal = now
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO accepted_operations(id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,created_at,terminal_at) VALUES(?,?,?,'admin',?,?,'',?,?)`, id, kind, admin, hash[:], state, now, terminal)
	return err
}
func (s *Service) Resume(ctx context.Context, admin int64, key string, input ResumeInput) (MutationResult[ResumeResult], error) {
	var out MutationResult[ResumeResult]
	revision, err := decimalRevision(input.ExpectedRevision, false)
	if err != nil || revision == math.MaxInt64 || !db.ValidateOpaqueID(input.ControlID, "iup_") || !safeReason(input.Reason) {
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
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPost, adminPrefix+"/upstream/resume", input, now)
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
	if err = requireOne(tx.ExecContext(ctx, `UPDATE image_upstream_control SET protection_paused=0,protection_reason='',protection_revision=protection_revision+1,updated_at=? WHERE id=? AND protection_revision=? AND protection_paused=1`, now, input.ControlID, revision)); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE image_activity_tasks SET slot_state='ignored',updated_at=? WHERE control_id=? AND slot_state='uncertain'", now, input.ControlID); err != nil {
		return out, err
	}
	op, err := newID("op_")
	if err != nil {
		return out, err
	}
	if err = acceptedTx(ctx, tx, op, "image_upstream_resume", admin, now, input, true); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO image_upstream_resume_audits(operation_id,control_id,actor_user_id,previous_revision,resulting_revision,reason,created_at) VALUES(?,?,?,?,?,?,?)`, op, input.ControlID, admin, revision, revision+1, input.Reason, now); err != nil {
		return out, err
	}
	out.Value.Control, err = controlTx(ctx, tx, input.ControlID)
	if err != nil {
		return out, err
	}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	s.signal()
	return out, nil
}
func (s *Service) GetAdminModel(ctx context.Context, admin int64, id string) (AdminModel, error) {
	if !db.ValidateOpaqueID(id, "imdl_") {
		return AdminModel{}, ErrInvalid
	}
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AdminModel{}, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return AdminModel{}, err
	}
	snapshot, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return AdminModel{}, err
	}
	m, err := modelTx(ctx, tx, id, 0)
	if err != nil {
		return AdminModel{}, err
	}
	if m.controlID != snapshot.controlID {
		return AdminModel{}, ErrNotFound
	}
	return adminModelView(m), tx.Commit()
}
