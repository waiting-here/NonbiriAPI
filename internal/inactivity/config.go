package inactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"math"
	"strconv"
)

func readPolicy(ctx context.Context, tx *sql.Tx) (Configuration, error) {
	var out Configuration
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT revision,config_json,decay_grace_until,protection_grace_until,updated_at FROM inactivity_policy WHERE id=1`).Scan(&out.Revision, &raw, &out.DecayGraceUntil, &out.ProtectionGraceUntil, &out.UpdatedAt)
	if err != nil {
		return out, err
	}
	out.Policy, err = decodePolicy([]byte(raw))
	return out, err
}
func (s *Service) beginAdmin(ctx context.Context, write bool) (*sql.Tx, int64, error) {
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorAdminSession {
		return nil, 0, ErrForbidden
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: !write})
	if err != nil {
		return nil, 0, err
	}
	if write {
		_, err = tx.ExecContext(ctx, `UPDATE inactivity_policy SET revision=revision WHERE id=1`)
	}
	if err == nil {
		err = s.auth.AuthorizeAdmin(ctx, tx, actor.UserID)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, 0, ErrForbidden
	}
	return tx, actor.UserID, nil
}
func (s *Service) Get(ctx context.Context) (Configuration, error) {
	tx, _, err := s.beginAdmin(ctx, false)
	if err != nil {
		return Configuration{}, err
	}
	defer tx.Rollback()
	out, err := readPolicy(ctx, tx)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

type Update struct {
	ExpectedRevision int64  `json:"expected_revision,string"`
	Policy           Policy `json:"policy"`
}

func (s *Service) Put(ctx context.Context, input Update, key string) (Configuration, error) {
	var out Configuration
	at := s.now().Unix()
	if input.ExpectedRevision < 1 || !validTime(at) || Validate(input.Policy) != nil {
		return out, ErrInvalid
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		return out, ErrInvalid
	}
	tx, actor, err := s.beginAdmin(ctx, true)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	payload, err := json.Marshal(input)
	if err != nil {
		return out, err
	}
	actorHash, _ := idempotency.ActorScopeHash("admin", strconv.FormatInt(actor, 10))
	hash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actorHash, Method: "PUT", Route: "/admin/api/inactivity-policy", Body: payload})
	if err != nil {
		return out, ErrInvalid
	}
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actorHash, Key: key, RequestHash: hash, DecisionNow: at})
	if err != nil {
		return out, err
	}
	if decision.Kind == idempotency.Replay {
		if json.Unmarshal(decision.ResponseBody, &out) != nil {
			return out, ErrInvalid
		}
		return out, tx.Commit()
	}
	old, err := readPolicy(ctx, tx)
	if err != nil {
		return out, err
	}
	if old.Revision != input.ExpectedRevision || old.Revision == math.MaxInt64 {
		return out, ErrConflict
	}
	d, b := grace(old, input.Policy, at)
	raw, err := json.Marshal(input.Policy)
	if err != nil || len(raw) > 4096 {
		return out, ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `UPDATE inactivity_policy SET revision=?,config_json=?,decay_grace_until=?,protection_grace_until=?,updated_at=?,updated_by=? WHERE id=1`, old.Revision+1, string(raw), d, b, at, actor)
	if err != nil {
		return out, err
	}
	out = Configuration{Policy: input.Policy, Revision: old.Revision + 1, DecayGraceUntil: d, ProtectionGraceUntil: b, UpdatedAt: at}
	audit, _ := json.Marshal(struct {
		Before Policy `json:"before"`
		After  Policy `json:"after"`
	}{old.Policy, input.Policy})
	if err = insertAudit(ctx, tx, actor, "configure", out.Revision, string(audit), at); err != nil {
		return out, err
	}
	response, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if err = idempotency.Complete(ctx, tx, decision, 200, response); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func insertAudit(ctx context.Context, tx *sql.Tx, actor int64, action string, revision int64, details string, at int64) error {
	id, err := db.GenerateOpaqueID("op_")
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO inactivity_audits(id,actor_user_id,action,policy_revision,details_json,created_at,deidentify_at,retain_until) VALUES(?,?,?,?,?,?,?,?)`, id, actor, action, revision, details, at, at+identityLife, at+auditLife)
	return err
}
