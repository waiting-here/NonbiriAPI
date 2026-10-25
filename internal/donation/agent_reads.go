package donation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

type agentReadInput struct {
	DonationID string `json:"donation_id,omitempty"`
	Status     string `json:"status,omitempty"`
	OwnerID    string `json:"owner_id,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Page       int    `json:"page,omitempty"`
	PageSize   int    `json:"page_size,omitempty"`
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
	if in.Page == 0 {
		in.Page = 1
	}
	if in.PageSize == 0 {
		in.PageSize = 50
	}
	if in.Page < 1 || in.Page > 10000 || in.PageSize < 1 || in.PageSize > 100 {
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
	var id, owner int64
	if in.DonationID != "" {
		id, err = parseID(in.DonationID)
		if err != nil {
			return nil, err
		}
	}
	if in.OwnerID != "" {
		owner, err = parseID(in.OwnerID)
		if err != nil {
			return nil, err
		}
		if access.scope.Redact {
			return nil, authz.ErrPermissionDenied
		}
	}
	offset := (in.Page - 1) * in.PageSize
	if tool == "donations" {
		if in.Decision != "" {
			return nil, ErrInvalidRequest
		}
		switch in.Status {
		case "", "pending", "approved", "rejected", "deleted", "expired":
		default:
			return nil, ErrInvalidRequest
		}
		predicate, values := charityscope.DonationPredicate(access.scope.DonationScope, now)
		query := `SELECT d.id FROM donations d WHERE ` + predicate
		bind := values
		if access.role != reviewerAdmin {
			query += ` AND (d.status IN ('pending','approved') OR d.terminal_at>?)`
			bind = append(bind, now-terminalRetention)
		}
		if id > 0 {
			query += ` AND d.id=?`
			bind = append(bind, id)
		}
		if owner > 0 {
			query += ` AND d.user_id=?`
			bind = append(bind, owner)
		}
		if in.Status != "" {
			query += ` AND d.status=?`
			bind = append(bind, in.Status)
		}
		query += ` ORDER BY d.id DESC LIMIT ? OFFSET ?`
		bind = append(bind, in.PageSize+1, offset)
		rows, err := tx.QueryContext(ctx, query, bind...)
		if err != nil {
			return nil, err
		}
		ids := []int64{}
		for rows.Next() {
			var n int64
			if err = rows.Scan(&n); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		more := len(ids) > in.PageSize
		if more {
			ids = ids[:in.PageSize]
		}
		data := []json.RawMessage{}
		for _, n := range ids {
			raw, e := s.agentReceiptTx(ctx, tx, access, n, now)
			if e != nil {
				return nil, e
			}
			var v map[string]any
			if e = json.Unmarshal(raw, &v); e != nil {
				return nil, e
			}
			if keys, ok := v["keys"].([]any); ok {
				for _, key := range keys {
					if k, ok := key.(map[string]any); ok {
						delete(k, "display_head")
						delete(k, "display_tail")
					}
				}
			}

			if id > 0 {
				_, signature, pendingErr := agentPendingTx(ctx, tx, n)
				if pendingErr == nil {
					v["signature"] = signature
				} else if !errors.Is(pendingErr, ErrConflict) {
					return nil, pendingErr
				}
				uses, e := agentSafeUsesTx(ctx, tx, access, n, now)
				if e != nil {
					return nil, e
				}
				v["uses"] = uses
			}
			raw, e = json.Marshal(v)
			if e != nil {
				return nil, e
			}
			data = append(data, raw)
		}
		return json.Marshal(struct {
			Data    []json.RawMessage `json:"data"`
			HasMore bool              `json:"has_more"`
		}{data, more})
	}
	if id <= 0 || owner != 0 || in.Status != "" {
		return nil, ErrInvalidRequest
	}
	if err = requireManagedDonationTx(ctx, tx, access.role, id, now); err != nil {
		return nil, err
	}
	if err = access.scope.RequireDonation(ctx, tx, id, now); err != nil {
		return nil, scopeError(err)
	}
	if tool == "review_history" {
		switch in.Decision {
		case "", "approve", "reject", "force_reject", "limit_update", "enable", "disable", "note_update", "withdraw", "terminate", "expire", "member_removed", "failure_streak_reset", "failure_policy_update":
		default:
			return nil, ErrInvalidRequest
		}
		query := `SELECT record_id,revision,reviewer_id,role,action,note,at,kind,use_id FROM (
 SELECT id AS sort_id,CAST(id AS TEXT) AS record_id,CAST(submission_revision AS TEXT) AS revision,CAST(reviewer_user_id AS TEXT) AS reviewer_id,reviewer_role AS role,action,note,created_at AS at,'donation' AS kind,NULL AS use_id FROM donation_reviews WHERE donation_id=?
 UNION ALL SELECT ur.id,CAST(ur.id AS TEXT),CAST(ur.use_revision AS TEXT),CAST(ur.reviewer_user_id AS TEXT),ur.reviewer_role,CASE ur.review_state WHEN 'approved' THEN 'approve' ELSE 'reject' END,ur.review_reason,ur.reviewed_at,'service',CAST(u.id AS TEXT) FROM donation_use_revisions ur JOIN donation_uses u ON u.id=ur.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE dk.donation_id=? AND ur.review_state IN ('approved','rejected') AND ur.reviewed_at IS NOT NULL
 ) WHERE (?='' OR action=?) ORDER BY at DESC,kind,sort_id DESC LIMIT ? OFFSET ?`
		bind := []any{id, id, in.Decision, in.Decision, in.PageSize + 1, offset}
		rows, err := tx.QueryContext(ctx, query, bind...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type review struct {
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
		for rows.Next() {
			var v review
			var reviewer, useID sql.NullString
			if err = rows.Scan(&v.ID, &v.Revision, &reviewer, &v.Role, &v.Action, &v.Note, &v.At, &v.Kind, &useID); err != nil {
				return nil, err
			}
			if !access.scope.Redact && reviewer.Valid {
				v.ReviewerID = &reviewer.String
			}
			if useID.Valid {
				v.UseID = &useID.String
			}
			data = append(data, v)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
		more := len(data) > in.PageSize
		if more {
			data = data[:in.PageSize]
		}
		return json.Marshal(struct {
			Data    []review `json:"data"`
			HasMore bool     `json:"has_more"`
		}{data, more})
	}
	if in.Decision != "" {
		return nil, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, `SELECT CAST(dk.id AS TEXT),m.normalized_model_id,m.display_name,'manual' FROM donation_key_manual_models m JOIN donation_keys dk ON dk.id=m.donation_key_id WHERE dk.donation_id=?
 UNION SELECT CAST(dk.id AS TEXT),c.normalized_model_id,c.normalized_model_id,'automatic' FROM donation_keys dk JOIN donation_uses u ON u.donation_key_id=dk.id JOIN donation_use_revisions ur ON ur.id IN (u.current_pending_revision_id,u.current_approved_revision_id) JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id JOIN discovery_source_states st ON st.source_revision_id=`+connectionstore.DiscoverySourceRevisionSQL+` JOIN model_catalog_entries c ON c.discovery_run_id=st.current_run_id AND c.source_type='automatic' WHERE dk.donation_id=? ORDER BY 1,2,4 LIMIT ? OFFSET ?`, id, id, in.PageSize+1, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	data := []map[string]string{}
	for rows.Next() {
		var key, model, name, source string
		if err = rows.Scan(&key, &model, &name, &source); err != nil {
			return nil, err
		}
		data = append(data, map[string]string{"key_id": key, "model": model, "name": name, "source": source})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	more := len(data) > in.PageSize
	if more {
		data = data[:in.PageSize]
	}
	return json.Marshal(struct {
		Data    []map[string]string `json:"data"`
		HasMore bool                `json:"has_more"`
	}{data, more})
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
