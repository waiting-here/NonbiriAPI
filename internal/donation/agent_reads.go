package donation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

type agentReadInput struct {
	DonationID    string `json:"donation_id,omitempty"`
	DonationKeyID string `json:"donation_key_id,omitempty"`
	Status        string `json:"status,omitempty"`
	OwnerID       string `json:"owner_id,omitempty"`
	Decision      string `json:"decision,omitempty"`
	Query         string `json:"query,omitempty"`
	Source        string `json:"source,omitempty"`
}

func (s *Service) AgentRead(ctx context.Context, tx *sql.Tx, actor agentplan.Actor, tool string, args json.RawMessage, authorize agentplan.Authorize) (json.RawMessage, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	if tool != "donations" && tool != "donation_models" && tool != "review_history" {
		return nil, ErrInvalidRequest
	}
	var in agentReadInput
	if err := agentDecode(args, &in); err != nil {
		return nil, err
	}
	if !utf8.ValidString(in.Query) || utf8.RuneCountInString(in.Query) > 512 ||
		(tool != "donations" && (in.OwnerID != "" || in.Status != "")) ||
		(tool != "review_history" && in.Decision != "") ||
		(tool != "donation_models" && (in.Query != "" || in.Source != "" || in.DonationKeyID != "")) {
		return nil, ErrInvalidRequest
	}
	access, err := s.agentAccess(ctx, tx, actor, agentplan.Operation{Kind: tool}, authorize)
	if err != nil {
		return nil, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, err
	}
	var id, owner, keyID int64
	for _, field := range []struct {
		value  string
		target *int64
	}{{in.DonationID, &id}, {in.OwnerID, &owner}, {in.DonationKeyID, &keyID}} {
		if field.value != "" {
			*field.target, err = parseID(field.value)
			if err != nil {
				return nil, err
			}
		}
	}
	if owner > 0 && access.scope.Redact {
		return nil, authz.ErrPermissionDenied
	}
	switch in.Status {
	case "", "pending", "approved", "rejected", "deleted", "expired":
	default:
		return nil, ErrInvalidRequest
	}
	// Every child read starts from the same authorized parent snapshot. A
	// missing donation_id widens the filter, never the actor's resource scope.
	predicate, values := charityscope.DonationPredicate(access.scope.DonationScope, now)
	selection := `SELECT d.id FROM donations d WHERE ` + predicate
	if access.role != reviewerAdmin {
		selection += ` AND (d.status IN ('pending','approved') OR d.terminal_at>?)`
		values = append(values, now-terminalRetention)
	}
	if id > 0 {
		selection += ` AND d.id=?`
		values = append(values, id)
	}
	if owner > 0 {
		selection += ` AND d.user_id=?`
		values = append(values, owner)
	}
	if in.Status != "" {
		selection += ` AND d.status=?`
		values = append(values, in.Status)
	}
	if tool == "donations" {
		return agentDonationsTx(ctx, tx, access, selection, values, now)
	}
	if tool == "review_history" {
		switch in.Decision {
		case "", "approve", "reject", "force_reject", "limit_update", "enable", "disable", "note_update", "withdraw", "terminate", "expire", "member_removed", "failure_streak_reset", "failure_policy_update":
		default:
			return nil, ErrInvalidRequest
		}
		query := `WITH visible_donations AS (` + selection + `)
SELECT donation_id,record_id,revision,reviewer_id,role,action,note,at,kind,use_id FROM (
 SELECT dr.donation_id,dr.id AS sort_id,CAST(dr.id AS TEXT) AS record_id,CAST(dr.submission_revision AS TEXT) AS revision,CAST(dr.reviewer_user_id AS TEXT) AS reviewer_id,dr.reviewer_role AS role,dr.action,dr.note,dr.created_at AS at,'donation' AS kind,NULL AS use_id FROM donation_reviews dr JOIN visible_donations vd ON vd.id=dr.donation_id
 UNION ALL SELECT dk.donation_id,ur.id,CAST(ur.id AS TEXT),CAST(ur.use_revision AS TEXT),CAST(ur.reviewer_user_id AS TEXT),ur.reviewer_role,CASE ur.review_state WHEN 'approved' THEN 'approve' ELSE 'reject' END,ur.review_reason,ur.reviewed_at,'service',CAST(u.id AS TEXT) FROM donation_use_revisions ur JOIN donation_uses u ON u.id=ur.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN visible_donations vd ON vd.id=dk.donation_id WHERE ur.review_state IN ('approved','rejected') AND ur.reviewed_at IS NOT NULL
 ) WHERE (?='' OR action=?) ORDER BY at DESC,donation_id DESC,kind,sort_id DESC`
		values = append(values, in.Decision, in.Decision)
		rows, err := tx.QueryContext(ctx, query, values...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type review struct {
			DonationID string  `json:"donation_id"`
			ID         string  `json:"id"`
			Kind       string  `json:"kind"`
			UseID      *string `json:"use_id,omitempty"`
			Revision   string  `json:"revision"`
			ReviewerID *string `json:"reviewer_id,omitempty"`
			Role       string  `json:"role"`
			Action     string  `json:"action"`
			Note       string  `json:"note"`
			At         int64   `json:"created_at"`
		}
		data := []review{}
		var budget agentplan.ReadBudget
		for rows.Next() {
			var v review
			var reviewer, useID sql.NullString
			if err = rows.Scan(&v.DonationID, &v.ID, &v.Revision, &reviewer, &v.Role, &v.Action, &v.Note, &v.At, &v.Kind, &useID); err != nil {
				return nil, err
			}
			if !access.scope.Redact && reviewer.Valid {
				v.ReviewerID = &reviewer.String
			}
			if useID.Valid {
				v.UseID = &useID.String
			}
			if err = budget.Add(v); err != nil {
				return nil, err
			}
			data = append(data, v)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return agentReadResult(data, len(data))
	}
	if in.Source != "" && in.Source != "manual" && in.Source != "automatic" {
		return nil, ErrInvalidRequest
	}
	query := `WITH visible_donations AS (` + selection + `), catalog AS (
 SELECT dk.donation_id,dk.id AS key_id,m.normalized_model_id AS model,m.display_name AS name,'manual' AS source FROM donation_key_manual_models m JOIN donation_keys dk ON dk.id=m.donation_key_id JOIN visible_donations vd ON vd.id=dk.donation_id
 UNION SELECT dk.donation_id,dk.id,c.normalized_model_id,c.normalized_model_id,'automatic' FROM donation_keys dk JOIN visible_donations vd ON vd.id=dk.donation_id JOIN donation_uses u ON u.donation_key_id=dk.id JOIN donation_use_revisions ur ON ur.id IN (u.current_pending_revision_id,u.current_approved_revision_id) JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id JOIN discovery_source_states st ON st.source_revision_id=` + connectionstore.DiscoverySourceRevisionSQL + ` JOIN model_catalog_entries c ON c.discovery_run_id=st.current_run_id AND c.source_type='automatic'
 ) SELECT CAST(donation_id AS TEXT),CAST(key_id AS TEXT),model,name,source FROM catalog WHERE (?=0 OR key_id=?) AND (?='' OR source=?) AND (?='' OR instr(lower(model),lower(?))>0 OR instr(lower(name),lower(?))>0) ORDER BY donation_id DESC,key_id,model,source`
	values = append(values, keyID, keyID, in.Source, in.Source, in.Query, in.Query, in.Query)
	rows, err := tx.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data := []map[string]string{}
	var budget agentplan.ReadBudget
	for rows.Next() {
		var donationID, key, model, name, source string
		if err = rows.Scan(&donationID, &key, &model, &name, &source); err != nil {
			return nil, err
		}
		item := map[string]string{"donation_id": donationID, "key_id": key, "model": model, "name": name, "source": source}
		if err = budget.Add(item); err != nil {
			return nil, err
		}
		data = append(data, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return agentReadResult(data, len(data))
}

func agentReadResult(data any, count int) (json.RawMessage, error) {
	return agentplan.MarshalRead(struct {
		Data  any    `json:"data"`
		Total string `json:"total"`
	}{data, strconv.Itoa(count)})
}

func agentDonationsTx(ctx context.Context, tx *sql.Tx, access useAccess, selection string, args []any, now int64) (json.RawMessage, error) {
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY d.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	ids, err := agentReadIDs(rows)
	if err != nil {
		return nil, err
	}
	scope, err := readDonationAccessScope(ctx, tx, access.role, access.actorID)
	if err != nil {
		return nil, err
	}
	data := make([]json.RawMessage, 0, len(ids))
	var budget agentplan.ReadBudget
	// Batches bound SQLite parameters and reuse summaries; every selected row
	// still belongs to this one read transaction and the result is complete.
	for start := 0; start < len(ids); start += 100 {
		batch := ids[start:min(start+100, len(ids))]
		summaries, err := readUseSummariesTx(ctx, tx, batch)
		if err != nil {
			return nil, err
		}
		statuses, err := readDonationServiceStatusesTx(ctx, tx, batch, now)
		if err != nil {
			return nil, err
		}
		for _, id := range batch {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			value, err := getDonationProjectionWithStatusesTx(ctx, tx, id, now, statuses, summaries[id])
			if err != nil {
				return nil, err
			}
			value, err = scope.projectReceiptTx(ctx, tx, value)
			if err != nil {
				return nil, err
			}
			var receipt any = value
			if access.role != reviewerAdmin {
				receipt = stewardFromAdmin(value, access.actorID)
			}
			raw, err := json.Marshal(receipt)
			if err != nil {
				return nil, err
			}
			var v map[string]any
			if err = json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			if keys, ok := v["keys"].([]any); ok {
				for _, key := range keys {
					if k, ok := key.(map[string]any); ok {
						delete(k, "display_head")
						delete(k, "display_tail")
					}
				}
			}
			if summaries[id].Pending != "0" {
				_, signature, pendingErr := agentPendingTx(ctx, tx, id)
				if pendingErr == nil {
					v["signature"] = signature
				} else if !errors.Is(pendingErr, ErrConflict) && !errors.Is(pendingErr, ErrNotFound) {
					return nil, pendingErr
				}
			}
			uses := []Use{}
			if summaries[id].Total != "0" {
				uses, err = agentSafeUsesTx(ctx, tx, access, id, now)
				if err != nil {
					return nil, err
				}
			}
			v["uses"] = uses
			raw, err = json.Marshal(v)
			if err != nil {
				return nil, err
			}
			if err = budget.AddEncoded(raw); err != nil {
				return nil, err
			}
			data = append(data, raw)
		}
	}
	return agentReadResult(data, len(data))
}

func agentReadIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if err := agentplan.CheckReadCount(len(ids) + 1); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func agentSafeUsesTx(ctx context.Context, tx *sql.Tx, access useAccess, id, now int64) ([]Use, error) {
	rows, err := tx.QueryContext(ctx, `SELECT u.id FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? AND dk.ended_at IS NULL ORDER BY u.id LIMIT ?`, id, maxDonationKeys*19+1)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > maxDonationKeys*19 {
		return nil, ErrResourceLimit
	}
	out := []Use{}
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		uses, err := readUsesTx(ctx, tx, access, id, ids[start:end], now)
		if err != nil {
			return nil, err
		}
		for i := range uses {
			for _, revision := range []*UseRevision{uses[i].Pending, uses[i].Approved, uses[i].Latest} {
				if revision != nil {
					revision.DisplayHead = ""
					revision.DisplayTail = ""
				}
			}
		}
		out = append(out, uses...)
	}
	return out, nil
}
