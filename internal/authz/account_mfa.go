package authz

import (
	"context"
	"database/sql"
	"errors"
)

type MFAState struct {
	Enrolled      bool   `json:"enrolled"`
	Required      bool   `json:"required"`
	Verified      bool   `json:"verified"`
	VerifiedUntil *int64 `json:"verified_until"`
}

// ReadMFAState reads current policy and proof in an identity-checked transaction.
func ReadMFAState(ctx context.Context, tx *sql.Tx, actor Actor, now int64) (MFAState, error) {
	var state MFAState
	var policy sql.NullString
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT cfg.value,c.user_id IS NOT NULL,p.verified_until
 FROM users u
 LEFT JOIN site_config cfg ON cfg.key='mfa_required_level_'||COALESCE(u.level,u.auto_level)
 LEFT JOIN account_mfa_credentials c ON c.user_id=u.id
 LEFT JOIN account_mfa_proofs p ON p.user_id=u.id AND p.session_hash=? AND p.session_generation=? AND p.verified_until>?
 WHERE u.id=?`, actor.SessionTokenHash, actor.SessionGeneration, now, actor.UserID).Scan(&policy, &state.Enrolled, &until)
	if err != nil {
		return state, err
	}
	if !policy.Valid || policy.String != "0" && policy.String != "1" {
		return state, errors.New("invalid account MFA policy")
	}
	state.Required = policy.String == "1"
	state.Verified = state.Enrolled && until.Valid
	if state.Verified {
		state.VerifiedUntil = &until.Int64
	}
	return state, nil
}
