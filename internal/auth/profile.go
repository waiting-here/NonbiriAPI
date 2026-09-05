package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type profilePatchWire struct {
	Lang              json.RawMessage `json:"lang"`
	GameProfilePublic json.RawMessage `json:"game_profile_public"`
}

type profilePatchCanonical struct {
	Lang              *string `json:"lang,omitempty"`
	GameProfilePublic *bool   `json:"game_profile_public,omitempty"`
}

func decodeProfilePatch(w http.ResponseWriter, req *http.Request) (profilePatchCanonical, []byte, bool) {
	var wire profilePatchWire
	if !decodeJSONBody(w, req, &wire, true) {
		return profilePatchCanonical{}, nil, false
	}
	var patch profilePatchCanonical
	if wire.Lang != nil {
		if bytes.Equal(bytes.TrimSpace(wire.Lang), []byte("null")) || json.Unmarshal(wire.Lang, &patch.Lang) != nil || patch.Lang == nil || (*patch.Lang != "zh" && *patch.Lang != "en") {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
			return profilePatchCanonical{}, nil, false
		}
	}
	if wire.GameProfilePublic != nil {
		if bytes.Equal(bytes.TrimSpace(wire.GameProfilePublic), []byte("null")) || json.Unmarshal(wire.GameProfilePublic, &patch.GameProfilePublic) != nil || patch.GameProfilePublic == nil {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
			return profilePatchCanonical{}, nil, false
		}
	}
	if patch.Lang == nil && patch.GameProfilePublic == nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return profilePatchCanonical{}, nil, false
	}
	canonical, err := idempotency.CanonicalJSON(patch)
	if err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return profilePatchCanonical{}, nil, false
	}
	return patch, canonical, true
}

func (r *Runtime) patchMe(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) {
		return
	}
	key, ok := singleHeader(req, "Idempotency-Key", true)
	if !ok {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid idempotency key")
		return
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid idempotency key")
		return
	}
	patch, canonical, ok := decodeProfilePatch(w, req)
	if !ok {
		return
	}
	actor, ok := ActorFromContext(req.Context())
	if !ok || actor.Kind != authz.ActorUserSession {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	actorHash, err := idempotency.ActorScopeHash("user", strconv.FormatInt(actor.UserID, 10))
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actorHash, Method: http.MethodPatch, Route: "/api/me", Query: "", Body: canonical})
	if err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	tx, err := r.db.BeginTx(req.Context(), nil)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if _, err = r.authorizer.Authorize(req.Context(), tx, actor, authz.Requirement{Role: authz.RoleUser}); err != nil {
		writeAuthorizationFailure(w, err)
		return
	}
	decision, err := idempotency.Begin(req.Context(), tx, idempotency.BeginInput{Scope: idempotency.ScopeControlMutation, ActorHash: actorHash, Key: key, RequestHash: digest, DecisionNow: r.now().Unix()})
	if err != nil {
		if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
			writeStableError(w, httperr.CodeConflict, "idempotency conflict")
		} else {
			writeStableError(w, httperr.CodeInternal, "profile update failed")
		}
		return
	}
	if decision.Kind == idempotency.Replay {
		if err := tx.Commit(); err != nil {
			writeStableError(w, httperr.CodeInternal, "profile update failed")
			return
		}
		done = true
		writeJSONBytes(w, decision.HTTPStatus, decision.ResponseBody)
		return
	}
	var revision []byte
	if err := tx.QueryRowContext(req.Context(), `SELECT revision FROM users WHERE id=? AND is_admin=0`, actor.UserID).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		} else {
			writeStableError(w, httperr.CodeInternal, "profile update failed")
		}
		return
	}
	next, err := incrementU128(revision)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	langSet := 0
	lang := ""
	if patch.Lang != nil {
		langSet = 1
		lang = *patch.Lang
	}
	publicSet := 0
	public := 0
	if patch.GameProfilePublic != nil {
		publicSet = 1
		if *patch.GameProfilePublic {
			public = 1
		}
	}
	result, err := tx.ExecContext(req.Context(), `UPDATE users SET lang=CASE WHEN ?=1 THEN ? ELSE lang END,game_profile_public=CASE WHEN ?=1 THEN ? ELSE game_profile_public END,revision=?,updated_at=? WHERE id=? AND revision=? AND is_admin=0`, langSet, lang, publicSet, public, next, r.now().Unix(), actor.UserID, revision)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		writeStableError(w, httperr.CodeConflict, "profile changed")
		return
	}
	if patch.GameProfilePublic != nil {
		if _, err := tx.ExecContext(req.Context(), `UPDATE game_user_preferences SET game_profile_public=?,updated_at=? WHERE user_id=?`, public, r.now().Unix(), actor.UserID); err != nil {
			writeStableError(w, httperr.CodeInternal, "profile update failed")
			return
		}
	}
	envelope, err := r.userEnvelopeTx(req.Context(), tx, actor.UserID, r.now().Unix())
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	body = append(body, '\n')
	if err := idempotency.Complete(req.Context(), tx, decision, http.StatusOK, body); err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	if err := tx.Commit(); err != nil {
		writeStableError(w, httperr.CodeInternal, "profile update failed")
		return
	}
	done = true
	writeJSONBytes(w, http.StatusOK, body)
}

func writeAuthorizationFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authz.ErrUnauthorized):
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
	case errors.Is(err, authz.ErrForbidden):
		writeStableError(w, httperr.CodeForbidden, "access forbidden")
	default:
		writeStableError(w, httperr.CodeInternal, "authorization failed")
	}
}
