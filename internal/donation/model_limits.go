package donation

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const routeOwnerModelLimits = "/api/donations/{id}/keys/{keyId}/model-limits"
const routeAdminModelLimits = "/admin/api/donations/{id}/keys/{keyId}/model-limits"
const routeStewardModelLimits = "/api/steward/donations/{id}/keys/{keyId}/model-limits"

// Scope every branch before UNION so another key's catalog/history is never
// scanned to enumerate this key's selectable upstream identities.
var quotaModelNames = `SELECT upstream_model_id FROM donation_model_quotas WHERE donation_key_id=?1
 UNION SELECT upstream_model_id FROM donation_quota_rules WHERE donation_key_id=?1 AND upstream_model_id<>'' AND current_epoch IS NOT NULL
 UNION SELECT upstream_model_id FROM charity_binding_targets WHERE donation_key_id=?1
 UNION SELECT normalized_model_id FROM donation_key_manual_models WHERE donation_key_id=?1
 UNION SELECT ac.normalized_model_id FROM model_catalog_entries ac
 WHERE ac.source_type='automatic' AND ac.discovery_run_id IN (
 SELECT r.id FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id
 JOIN donation_use_revisions ur ON (ur.id=u.current_approved_revision_id OR ur.id=u.current_pending_revision_id) AND ur.use_id=u.id
 JOIN (SELECT g.id,g.owner_user_id,g.credential_id,g.credential_version_id,e.connector_type AS configured_type,e.base_url AS canonical_base_url
 FROM endpoint_key_grant_revisions g JOIN endpoints e ON e.id=g.endpoint_id) a ON a.id=ur.grant_revision_id
 JOIN discovery_source_states st ON st.source_revision_id=` + connectionstore.DiscoverySourceRevisionSQL + `
 JOIN discovery_runs r ON r.id=st.last_successful_run_id AND r.source_revision_id=st.source_revision_id AND r.credential_version_id=a.credential_version_id AND r.state='succeeded'
 WHERE u.donation_key_id=?1)`

func requireQuotaModel(ctx context.Context, tx *sql.Tx, keyID int64, model string) error {
	if !donationquota.ValidModel(model) {
		return ErrInvalidRequest
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (`+quotaModelNames+`) WHERE upstream_model_id=?2)`, keyID, model).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

type ModelLimitsPage struct {
	RecurringReceipt
	Page[donationquota.ModelView]
}

func (s *Service) modelLimits(ctx context.Context, role reviewerRole, userID, donationID, keyID int64, page pagination.Request, query, model string) (ModelLimitsPage, error) {
	var out ModelLimitsPage
	if ctx == nil || donationID <= 0 || keyID <= 0 || !page.Valid() || len(query) > 2048 || (model != "" && !donationquota.ValidModel(model)) {
		return out, ErrInvalidRequest
	}
	if role == reviewerSteward {
		ctx = authz.WithStewardPermission(ctx, "donations.recurring")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var tx *sql.Tx
	var err error
	if role == recurringOwner {
		tx, err = s.beginBrowseTx(ctx, role, userID)
	} else {
		tx, _, err = s.beginScopedTx(ctx, role, userID, true)
	}
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	revision, err := s.recurringScope(ctx, tx, role, userID, donationID, keyID, now, true)
	if err != nil {
		return out, err
	}
	selection := `SELECT upstream_model_id FROM (` + quotaModelNames + `) WHERE instr(lower(upstream_model_id),lower(?2))>0 AND (?3='' OR upstream_model_id=?3)`
	args := []any{keyID, query, model}
	var count int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`)`, args...).Scan(&count); err != nil {
		return out, err
	}
	meta, offset, err := page.Window(count)
	if err != nil {
		return out, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY upstream_model_id LIMIT ?4 OFFSET ?5`, append(args, page.Size, offset)...)
	if err != nil {
		return out, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return out, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.RecurringReceipt = recurringReceipt(donationID, keyID, revision)
	out.Page = Page[donationquota.ModelView]{Data: make([]donationquota.ModelView, 0, len(names)), Pagination: &meta}
	for _, name := range names {
		value, err := donationquota.ModelUsage(ctx, tx, keyID, name)
		if err != nil {
			return out, quotaError(err)
		}
		out.Data = append(out.Data, value)
	}
	return out, tx.Commit()
}

func (s *Service) replaceModelLimits(ctx context.Context, role reviewerRole, userID, donationID, keyID int64, mutation resources.ControlMutation, expected int64, model string, limits donationquota.ModelLimits) (resources.MutationResult[RecurringReceipt], error) {
	var empty resources.MutationResult[RecurringReceipt]
	route := routeAdminModelLimits
	if role == reviewerSteward {
		route = routeStewardModelLimits
	}
	if ctx == nil || expected <= 0 || !donationquota.ValidModel(model) || donationquota.ValidateModelLimits(limits) != nil || !validScopedMutation(ctx, mutation, http.MethodPut, route, donationID, keyID) {
		return empty, ErrInvalidRequest
	}
	if role == reviewerSteward {
		ctx = authz.WithStewardPermission(ctx, "donations.recurring")
	}
	tx, scope, err := s.beginScopedTx(ctx, role, userID, false)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if _, err = s.recurringScope(ctx, tx, role, userID, donationID, keyID, now, false); err != nil {
		return empty, err
	}
	actorID, auditRole := scope.ActorID, scope.AuditRole(role == reviewerAdmin)
	decision, err := beginMutation(ctx, tx, auditRole, actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[RecurringReceipt](decision)
	}
	if err = requireQuotaModel(ctx, tx, keyID, model); err != nil {
		return empty, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donations SET revision=revision+1,updated_at=? WHERE id=? AND revision=? AND revision<9223372036854775807 AND status IN ('pending','approved') AND EXISTS(SELECT 1 FROM donation_keys k WHERE k.id=? AND k.donation_id=donations.id AND k.ended_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>?))`, now, donationID, expected, keyID, now)
	if err != nil {
		return empty, err
	}
	if err = requireOne(result); err != nil {
		return empty, err
	}
	if err = donationquota.SetModelLimits(ctx, tx, keyID, now, model, limits); err != nil {
		return empty, quotaError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at) VALUES(?,?,?,?,'limit_update','',?)`, donationID, expected+1, actorID, auditRole, now); err != nil {
		return empty, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, recurringReceipt(donationID, keyID, expected+1))
	if err != nil {
		return empty, err
	}
	if err = commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

func (api *httpAPI) modelLimitsHandler(role reviewerRole) AuthorizedUserHandler {
	return func(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
		id, ok := parsePathID(w, r, "id")
		if !ok {
			return
		}
		key, ok := parsePathID(w, r, "keyId")
		if !ok {
			return
		}
		if r.Method == http.MethodGet {
			values, ok := requestQuery(w, r)
			if !ok || !requireNoBody(w, r) {
				return
			}
			page, err := parseNumberedQuery(values, "q", "upstream_model_id")
			if err != nil {
				writeDonationError(w, err)
				return
			}
			out, err := api.service.modelLimits(r.Context(), role, p.UserID, id, key, page, values.Get("q"), values.Get("upstream_model_id"))
			if err != nil {
				writeDonationError(w, err)
				return
			}
			writeJSON(w, out)
			return
		}
		if !requireEmptyQuery(w, r) {
			return
		}
		var wire struct {
			ExpectedRevision requiredField[string] `json:"expected_revision"`
			UpstreamModelID  requiredField[string] `json:"upstream_model_id"`
			Tokens           nullableField[string] `json:"tokens"`
			InputTokens      nullableField[string] `json:"input_tokens"`
			OutputTokens     nullableField[string] `json:"output_tokens"`
		}
		if !decodeStrictObject(w, r, &wire) {
			return
		}
		revision, err := requiredRevision(wire.ExpectedRevision)
		if err != nil || !wire.UpstreamModelID.Set || !wire.Tokens.Set || !wire.InputTokens.Set || !wire.OutputTokens.Set {
			writeDonationError(w, ErrInvalidRequest)
			return
		}
		limits := donationquota.ModelLimits{Tokens: wire.Tokens.Value, InputTokens: wire.InputTokens.Value, OutputTokens: wire.OutputTokens.Value}
		route := routeAdminModelLimits
		if role == reviewerSteward {
			route = routeStewardModelLimits
		}
		canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value, "upstream_model_id": wire.UpstreamModelID.Value, "tokens": limits.Tokens, "input_tokens": limits.InputTokens, "output_tokens": limits.OutputTokens}
		mutation, ok := mutationFor(w, r, route, []int64{id, key}, canonical)
		if !ok {
			return
		}
		out, err := api.service.replaceModelLimits(r.Context(), role, p.UserID, id, key, mutation, revision, wire.UpstreamModelID.Value, limits)
		if err != nil {
			writeDonationError(w, err)
			return
		}
		writeMutation(w, out)
	}
}
