package imageactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type ProfileState struct {
	Revision string             `json:"revision"`
	Profile  *CapabilityProfile `json:"profile"`
}

type ProfileInput struct {
	ExpectedRevision string            `json:"expected_revision"`
	Profile          CapabilityProfile `json:"profile"`
}

func profileTx(ctx context.Context, tx *sql.Tx, control string) (ProfileState, error) {
	out := ProfileState{Revision: "0"}
	var revision int64
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT revision,profile_json FROM image_capability_profiles WHERE control_id=?`, control).Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	profile, err := ParseCapabilityProfile([]byte(raw))
	if err != nil {
		return out, ErrInvariant
	}
	out.Revision, out.Profile = strconv.FormatInt(revision, 10), &profile
	return out, nil
}

func (s *Service) GetProfile(ctx context.Context, admin int64) (ProfileState, error) {
	var out ProfileState
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, admin); err != nil {
		return out, err
	}
	upstream, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	out, err = profileTx(ctx, tx, upstream.controlID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) PutProfile(ctx context.Context, admin int64, key string, input ProfileInput) (MutationResult[ProfileState], error) {
	var out MutationResult[ProfileState]
	if err := ValidateCapabilityProfile(input.Profile); err != nil {
		return out, err
	}
	expected, err := decimalRevision(input.ExpectedRevision, true)
	if err != nil {
		return out, err
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
	d, err := s.beginReplay(ctx, tx, "admin", admin, key, http.MethodPut, adminPrefix+"/upstream/capability-profile", input, now)
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
	upstream, err := currentUpstreamTx(ctx, tx)
	if err != nil {
		return out, err
	}
	current, err := profileTx(ctx, tx, upstream.controlID)
	if err != nil {
		return out, err
	}
	if current.Revision != strconv.FormatInt(expected, 10) {
		return out, ErrConflict
	}
	if expected == int64(^uint64(0)>>1) {
		return out, ErrCapacity
	}
	raw, err := json.Marshal(input.Profile)
	if err != nil || len(raw) > 262144 {
		return out, ErrInvalid
	}
	if _, err = ParseCapabilityProfile(raw); err != nil {
		return out, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO image_capability_profiles(control_id,revision,profile_json,actor_user_id,updated_at)
	 VALUES(?,?,?,?,?) ON CONFLICT(control_id) DO UPDATE SET revision=excluded.revision,profile_json=excluded.profile_json,actor_user_id=excluded.actor_user_id,updated_at=excluded.updated_at
	 WHERE image_capability_profiles.revision=?`, upstream.controlID, expected+1, string(raw), admin, now, expected)
	if err != nil {
		return out, err
	}
	if changed, e := result.RowsAffected(); e != nil || changed != 1 {
		return out, ErrConflict
	}
	out.Value = ProfileState{Revision: strconv.FormatInt(expected+1, 10), Profile: &input.Profile}
	if err = finishReplay(ctx, tx, d, out.Value); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
