package charityrouting

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
)

// These menu projections expose only shared configuration facts. Donation
// management endpoints retain their separate, narrower ownership policy.
type BindingDonation struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	KeyCount    int    `json:"key_count"`
}

type BindingSourceKey struct {
	DonationKeyID string          `json:"donation_key_id"`
	Source        CandidateSource `json:"source"`
	Note          string          `json:"note"`
}

const bindingSourceFrom = ` FROM charity_models cm
JOIN donations d
JOIN donation_keys dk ON dk.donation_id=d.id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id
WHERE cm.id=? AND d.status='approved' AND d.user_id IS NOT NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND EXISTS(SELECT 1 FROM model_pair_catalog pc WHERE pc.endpoint_key_id=k.id
 AND (pc.automatic_supports>0 OR pc.manual_supports>0)
 AND NOT EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.charity_model_id=cm.id
  AND b.donation_key_id=dk.id AND b.upstream_model_id=pc.normalized_model_id))`

func (s *Service) bindingSources(ctx context.Context, role roleKind, actorID, modelID, donationID, afterID int64, limit int) ([]BindingDonation, []BindingSourceKey, int64, error) {
	if s == nil || ctx == nil || modelID <= 0 || donationID < 0 || afterID < 0 || limit < 1 || limit > maxPageLimit || (role != roleAdmin && role != roleSteward) || role == roleSteward && actorID <= 0 {
		return nil, nil, 0, ErrInvalidRequest
	}
	if s.db == nil || nilDependency(s.donationState) {
		return nil, nil, 0, ErrUnavailable
	}
	now, err := s.nowUnix()
	if err != nil {
		return nil, nil, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	defer tx.Rollback()
	if role == roleSteward {
		if nilDependency(s.roleAuth) {
			return nil, nil, 0, ErrUnavailable
		}
		if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorID); err != nil {
			return nil, nil, 0, mapAuthorization(err)
		}
	}
	if _, err := getAdminModelTx(ctx, tx, modelID); err != nil {
		return nil, nil, 0, err
	}
	if err := s.donationState.MaterializeDueExpiriesTx(ctx, tx, now, 100); err != nil {
		return nil, nil, 0, err
	}
	query := `SELECT d.id,d.description,COUNT(DISTINCT dk.id)` + bindingSourceFrom + ` AND d.id>? GROUP BY d.id,d.description ORDER BY d.id LIMIT ?`
	args := []any{modelID, now, afterID, limit + 1}
	if donationID != 0 {
		query = `SELECT dk.id,dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,dk.safe_note,COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0)` + bindingSourceFrom + ` AND d.id=? AND dk.id>? ORDER BY dk.id LIMIT ?`
		args = []any{modelID, now, donationID, afterID, limit + 1}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("charity routing: read binding sources: %w", err)
	}
	defer rows.Close()
	donations := make([]BindingDonation, 0, limit)
	keys := make([]BindingSourceKey, 0, limit)
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		if donationID == 0 {
			var entry BindingDonation
			if err := rows.Scan(&id, &entry.Description, &entry.KeyCount); err != nil {
				return nil, nil, 0, err
			}
			entry.ID = strconv.FormatInt(id, 10)
			donations = append(donations, entry)
		} else {
			var entry BindingSourceKey
			if err := rows.Scan(&id, &entry.Source.ConnectorType, &entry.Source.CanonicalBaseURL, &entry.Source.DisplayHead, &entry.Source.DisplayTail, &entry.Note, &entry.Source.MaxConcurrency, &entry.Source.MaxRPM); err != nil {
				return nil, nil, 0, err
			}
			entry.DonationKeyID = strconv.FormatInt(id, 10)
			keys = append(keys, entry)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	rows.Close()
	next := int64(0)
	if len(ids) > limit {
		next = ids[limit-1]
		if donationID == 0 {
			donations = donations[:limit]
		} else {
			keys = keys[:limit]
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, 0, err
	}
	return donations, keys, next, nil
}

func (api *httpAPI) adminBindingDonations(w http.ResponseWriter, r *http.Request) {
	api.bindingSources(w, r, roleAdmin, 0, false)
}
func (api *httpAPI) adminBindingKeys(w http.ResponseWriter, r *http.Request) {
	api.bindingSources(w, r, roleAdmin, 0, true)
}
func (api *httpAPI) stewardBindingDonations(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.bindingSources(w, r, roleSteward, p.UserID, false)
}
func (api *httpAPI) stewardBindingKeys(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.bindingSources(w, r, roleSteward, p.UserID, true)
}

func (api *httpAPI) bindingSources(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64, keys bool) {
	modelID, ok := parsePathID(w, r, "id")
	if !ok || !requireNoBody(w, r) {
		return
	}
	donationID := int64(0)
	if keys {
		donationID, ok = parsePathID(w, r, "donationId")
		if !ok {
			return
		}
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := parsePage(values, "cursor", "limit")
	if !ok {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	now, err := api.service.nowUnix()
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	scope := string(role) + "-charity-binding-donations"
	if keys {
		scope = string(role) + "-charity-binding-keys"
	}
	owner := paginationOwner(role, actorID, strconv.FormatInt(modelID, 10), strconv.FormatInt(donationID, 10))
	after, err := api.service.decodeModelCursor(cursor, scope, owner, now)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	donations, sourceKeys, next, err := api.service.bindingSources(r.Context(), role, actorID, modelID, donationID, after, limit)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	var nextCursor *string
	if next != 0 {
		value, err := api.service.encodeModelCursor(scope, owner, now, next)
		if err != nil {
			writeRoutingError(w, err)
			return
		}
		nextCursor = &value
	}
	if keys {
		writeJSON(w, Page[BindingSourceKey]{Data: sourceKeys, NextCursor: nextCursor})
	} else {
		writeJSON(w, Page[BindingDonation]{Data: donations, NextCursor: nextCursor})
	}
}
