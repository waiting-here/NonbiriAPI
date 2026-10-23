package donation

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

type useAccess struct {
	role       reviewerRole
	actorID    int64
	scope      charityscope.Scope
	reviewOnly bool
}

func (s *Service) beginUseTx(ctx context.Context, role reviewerRole, actorID int64, readOnly bool) (*sql.Tx, useAccess, error) {
	a := useAccess{role: role, actorID: actorID}
	var tx *sql.Tx
	var err error
	if role == recurringOwner {
		tx, err = s.beginOwnerTx(ctx, actorID)
	} else if role == reviewerSteward && charityscope.ModelID(ctx) > 0 {
		tx, a.scope, err = s.beginScopedTx(ctx, role, actorID, readOnly)
		a.actorID = a.scope.ActorID
	} else {
		tx, a.role, a.actorID, err = s.beginReviewTx(ctx, role, actorID, readOnly)
		a.reviewOnly = a.role == reviewerTrainee
	}
	return tx, a, err
}

func (a useAccess) predicate() (string, []any) {
	if a.role == recurringOwner {
		return ` AND d.user_id=?`, []any{a.actorID}
	}
	if a.reviewOnly {
		return ` AND du.current_pending_revision_id IS NOT NULL`, nil
	}
	return "", nil
}

func (s *Service) UsesPage(ctx context.Context, role reviewerRole, actorID, donationID int64, filter UseFilter, page pagination.Request) (Page[Use], error) {
	var empty Page[Use]
	if ctx == nil || donationID <= 0 || filter.CredentialID < 0 || filter.DonationKeyID < 0 || !page.Valid() {
		return empty, ErrInvalidRequest
	}
	switch filter.State {
	case "", "pending", "approved", "rejected", "withdrawn", "superseded":
	default:
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, access, err := s.beginUseTx(ctx, role, actorID, false)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if role == recurringOwner {
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations WHERE id=? AND user_id=?)`, donationID, actorID).Scan(&owned); err != nil {
			return empty, err
		}
		if !owned {
			return empty, ErrNotFound
		}
	}
	if access.reviewOnly {
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations d JOIN donation_keys dk ON dk.donation_id=d.id JOIN donation_uses u ON u.donation_key_id=dk.id WHERE d.id=? AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND dk.ended_at IS NULL AND u.current_pending_revision_id IS NOT NULL)`, donationID).Scan(&pending); err != nil {
			return empty, err
		}
		if !pending {
			return empty, ErrNotFound
		}
	}
	if _, err := materializeDonationExpiryTx(ctx, tx, donationID, now); err != nil {
		return empty, err
	}
	visible, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
	if err != nil {
		return empty, err
	}
	if access.role == reviewerAdmin || !visible && access.role == reviewerSteward && !access.reviewOnly {
		held, err := s.managementHeldRead(ctx, tx, access.role, access.actorID, donationID, now)
		if err != nil {
			return empty, err
		}
		visible = visible || held
	}
	if !visible {
		return empty, ErrNotFound
	}
	query := `SELECT du.id FROM donation_uses du JOIN donation_keys dk ON dk.id=du.donation_key_id JOIN donations d ON d.id=dk.donation_id WHERE d.id=?`
	args := []any{donationID}
	predicate, values := access.predicate()
	query += predicate
	args = append(args, values...)
	if access.scope.Trainee {
		query += ` AND ` + charityscope.KeyPredicate()
		args = append(args, access.scope.ModelID, now)
	}
	if filter.State != "" {
		switch filter.State {
		case "pending":
			query += ` AND du.current_pending_revision_id IS NOT NULL`
		case "approved":
			query += ` AND du.current_approved_revision_id IS NOT NULL`
		default:
			state := filter.State
			if state == "withdrawn" {
				state = "cancelled"
			}
			query += ` AND EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.id=(SELECT lr.id FROM donation_use_revisions lr WHERE lr.use_id=du.id AND lr.review_state IN ('rejected','cancelled','superseded') ORDER BY lr.use_revision DESC LIMIT 1) AND ur.review_state=?)`
			args = append(args, state)
		}
	}
	if filter.CredentialID > 0 {
		query += ` AND EXISTS(SELECT 1 FROM donation_use_revisions ur WHERE ur.use_id=du.id AND ur.credential_id_snapshot=? AND (ur.id=du.current_approved_revision_id OR ur.id=du.current_pending_revision_id OR ur.id=(SELECT lr.id FROM donation_use_revisions lr WHERE lr.use_id=du.id ORDER BY lr.use_revision DESC LIMIT 1)))`
		args = append(args, filter.CredentialID)
	}
	if filter.DonationKeyID > 0 {
		query += ` AND du.donation_key_id=?`
		args = append(args, filter.DonationKeyID)
	}
	ids, metadata, err := browseIDs(ctx, tx, query, args, page)
	if err != nil {
		return empty, err
	}
	items, err := readUsesTx(ctx, tx, access, donationID, ids, now)
	if err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return Page[Use]{Data: items, Pagination: &metadata}, nil
}

func decimalPointer(value sql.NullInt64) *string {
	if !value.Valid {
		return nil
	}
	v := strconv.FormatInt(value.Int64, 10)
	return &v
}
func unixPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func readUsesTx(ctx context.Context, tx *sql.Tx, access useAccess, donationID int64, ids []int64, now int64) ([]Use, error) {
	out := make([]Use, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		return nil, ErrInvalidRequest
	}
	args := []any{now, now, now, now, now, donationID}
	for _, id := range ids {
		args = append(args, id)
	}
	query := `SELECT du.id,dk.id,dk.endpoint_key_id,du.native_service_bit,du.enabled,du.discovery_enabled,du.revision,
du.current_approved_revision_id,du.current_pending_revision_id,(SELECT lr.id FROM donation_use_revisions lr WHERE lr.use_id=du.id AND lr.review_state IN ('rejected','cancelled','superseded') ORDER BY lr.use_revision DESC LIMIT 1),
d.status IN ('pending','approved') AND d.terminal_at IS NULL AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) AND EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id),
COALESCE((` + discoveryEligibleSQL + `),0),EXISTS(SELECT 1 FROM charity_use_availability av WHERE av.use_id=du.id),h.failure_streak,h.failure_disabled,NULLIF(h.retry_after_until,0),NULLIF((` + smartbackoff.ServiceUntilSQL("h") + `),0),
COALESCE(h.failure_disabled,0)=0 AND COALESCE(h.retry_after_until,0)<=? AND COALESCE((` + smartbackoff.ServiceUntilSQL("h") + `),0)<=?
FROM donation_uses du JOIN donation_keys dk ON dk.id=du.donation_key_id JOIN donations d ON d.id=dk.donation_id
LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN users u ON u.id=d.user_id AND u.id=e.user_id
LEFT JOIN donation_use_revisions ar ON ar.id=du.current_approved_revision_id LEFT JOIN endpoint_key_grant_revisions ag ON ag.id=ar.grant_revision_id
LEFT JOIN donation_service_health h ON h.use_id=du.id AND h.endpoint_key_id=dk.endpoint_key_id AND h.native_service_bit=du.native_service_bit AND h.credential_version_id=ag.credential_version_id
WHERE d.id=? AND du.id IN (` + strings.TrimRight(strings.Repeat("?,", len(ids)), ",") + `) ORDER BY du.id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type rowState struct {
		approved, pending, latest            sql.NullInt64
		active, eligible, available, healthy bool
	}
	states := []rowState{}
	revisionIDs := []int64{}
	for rows.Next() {
		var value Use
		var state rowState
		var id, keyID, bit, revision int64
		var endpoint sql.NullInt64
		var streak []byte
		var disabled sql.NullBool
		var retry, backoff sql.NullInt64
		if err := rows.Scan(&id, &keyID, &endpoint, &bit, &value.Enabled, &value.DiscoveryEnabled, &revision, &state.approved, &state.pending, &state.latest, &state.active, &state.eligible, &state.available, &streak, &disabled, &retry, &backoff, &state.healthy); err != nil {
			rows.Close()
			return nil, err
		}
		services, err := contract.NativeServicesFromMask(bit)
		if err != nil || len(services.Services()) != 1 {
			rows.Close()
			return nil, ErrInvariant
		}
		value.ID, value.DonationID, value.DonationKeyID, value.Revision = strconv.FormatInt(id, 10), strconv.FormatInt(donationID, 10), strconv.FormatInt(keyID, 10), strconv.FormatInt(revision, 10)
		value.EndpointKeyID = decimalPointer(endpoint)
		value.NativeService = services.Services()[0]
		if access.reviewOnly || access.scope.Trainee {
			value.EndpointKeyID = nil
		}
		if disabled.Valid {
			count, err := db.DecodeU128(streak)
			if err != nil {
				rows.Close()
				return nil, ErrInvariant
			}
			value.Health = &UseHealth{count.Decimal(), disabled.Bool, unixPointer(retry), unixPointer(backoff)}
		}
		for _, ref := range []sql.NullInt64{state.approved, state.pending, state.latest} {
			if ref.Valid {
				revisionIDs = append(revisionIDs, ref.Int64)
			}
		}
		out = append(out, value)
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	revisions, err := readUseRevisionsTx(ctx, tx, revisionIDs)
	if err != nil {
		return nil, err
	}
	for index := range out {
		value, state := &out[index], states[index]
		value.Approved, value.Pending, value.Latest = revisions[state.approved.Int64], revisions[state.pending.Int64], revisions[state.latest.Int64]
		if state.approved.Valid && value.Approved == nil || state.pending.Valid && value.Pending == nil || state.latest.Valid && value.Latest == nil {
			return nil, ErrInvariant
		}
		canMaintain := !access.reviewOnly
		value.Capabilities.CanManage = canMaintain && state.active
		value.Capabilities.CanReview = access.role != recurringOwner && value.Pending != nil && state.active && value.Pending.Review.MaterialAvailable && value.Pending.available
		value.Capabilities.CanResetFailures = canMaintain && state.active && value.Approved != nil
		usable := state.active && state.eligible && state.available && state.healthy && value.Approved != nil && !value.Approved.Review.Required
		value.Capabilities.CanBind = canMaintain && usable
		value.Capabilities.CanDiscover = canMaintain && usable && value.DiscoveryEnabled && value.Approved.discoveryAvailable
		if !usable {
			reason := "unavailable"
			switch {
			case !state.active:
				reason = "ended"
			case value.Approved == nil:
				reason = "not_approved"
			case !value.Enabled:
				reason = "disabled"
			case value.Approved.Review.Required:
				reason = "review_required"
			case value.Health != nil && value.Health.FailureDisabled:
				reason = "failure_disabled"
			case !state.healthy:
				reason = "rate_limited"
			}
			value.Capabilities.UnavailableReason = &reason
		}
	}
	return out, nil
}

func readUseRevisionsTx(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]*UseRevision, error) {
	out := make(map[int64]*UseRevision, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, `SELECT ur.id,ur.credential_id_snapshot,ur.credential_version,ur.grant_revision,ur.review_state,ur.review_reason,ur.reviewed_at,rr.revision,
sr.source_id,sr.source_revision,ur.display_head,ur.display_tail,COALESCE(rr.required,0),length(ur.key_body_review_hmac)=32,
COALESCE(a.available=1 AND a.relationship_valid=1 AND (a.effective_native_service_mask&du.native_service_bit)=du.native_service_bit,0),COALESCE(sa.available=1 AND sa.relationship_valid=1 AND sa.credential_version_id=a.credential_version_id AND sa.owner_user_id=a.owner_user_id,0),
COALESCE(r.state,'unknown'),MAX(COALESCE(st.result_generation,0),1),COALESCE(r.safe_class,'none'),r.started_at,r.completed_at,COALESCE(r.model_count,0)
FROM donation_use_revisions ur JOIN donation_uses du ON du.id=ur.use_id LEFT JOIN donation_key_review_requirements rr ON rr.hmac=ur.key_body_review_hmac
LEFT JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id LEFT JOIN discovery_source_revisions sr ON sr.id=ur.discovery_source_revision_id
LEFT JOIN discovery_source_authorizations sa ON sa.source_revision_id=sr.id
LEFT JOIN discovery_source_states st ON st.source_revision_id=sr.id LEFT JOIN discovery_runs r ON r.id=st.current_run_id AND r.source_revision_id=sr.id
WHERE ur.id IN (`+strings.TrimRight(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v UseRevision
		var id, credential, version, grant, generation, count int64
		var reviewed, requirement, source, sourceRevision, started, completed sql.NullInt64
		var material sql.NullBool
		var state, safe string
		if err := rows.Scan(&id, &credential, &version, &grant, &v.Status, &v.ReviewNote, &reviewed, &requirement, &source, &sourceRevision, &v.DisplayHead, &v.DisplayTail, &v.Review.Required, &material, &v.available, &v.discoveryAvailable, &state, &generation, &safe, &started, &completed, &count); err != nil {
			return nil, err
		}
		v.CredentialID, v.CredentialVersion, v.GrantRevision = strconv.FormatInt(credential, 10), strconv.FormatInt(version, 10), strconv.FormatInt(grant, 10)
		v.ReviewedAt = unixPointer(reviewed)
		v.ExpectedReviewRevision = decimalPointer(requirement)
		v.Review.Revision = decimalPointer(requirement)
		v.Review.MaterialAvailable = material.Valid && material.Bool
		v.DiscoverySourceID, v.DiscoverySourceRevision = decimalPointer(source), decimalPointer(sourceRevision)
		if v.Status == "cancelled" {
			v.Status = "withdrawn"
		}
		if source.Valid {
			v.Discovery = &resources.DiscoveryEvidence{State: state, Revision: strconv.FormatInt(generation, 10), SafeClass: safe}
			switch state {
			case "unknown":
			case "checking":
				v.Discovery.ObservedAt = unixPointer(started)
			case "failed":
				v.Discovery.ObservedAt = unixPointer(completed)
			case "succeeded":
				result := "nonempty"
				if count == 0 {
					result = "empty"
				}
				total := strconv.FormatInt(count, 10)
				v.Discovery.Result, v.Discovery.Count, v.Discovery.ObservedAt = &result, &total, unixPointer(completed)
			default:
				return nil, ErrInvariant
			}
		}
		out[id] = &v
	}
	return out, rows.Err()
}

func readOneUseTx(ctx context.Context, tx *sql.Tx, access useAccess, donationID, useID, now int64) (Use, error) {
	rows, err := readUsesTx(ctx, tx, access, donationID, []int64{useID}, now)
	if err != nil {
		return Use{}, err
	}
	if len(rows) != 1 {
		return Use{}, ErrNotFound
	}
	return rows[0], nil
}

func requireUseAccessTx(ctx context.Context, tx *sql.Tx, access useAccess, donationID, useID, now int64, active bool) error {
	query := `SELECT dk.id,d.status IN ('pending','approved') AND d.terminal_at IS NULL AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) AND EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_id=d.id AND m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id) FROM donation_uses du JOIN donation_keys dk ON dk.id=du.donation_key_id JOIN donations d ON d.id=dk.donation_id WHERE d.id=? AND du.id=?`
	args := []any{now, donationID, useID}
	predicate, values := access.predicate()
	query += predicate
	args = append(args, values...)
	var keyID int64
	var eligible bool
	err := tx.QueryRowContext(ctx, query, args...).Scan(&keyID, &eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if access.scope.Trainee {
		if err := access.scope.RequireKey(ctx, tx, donationID, keyID, now, false); err != nil {
			return scopeError(err)
		}
	}
	if active && !eligible {
		return ErrConflict
	}
	return nil
}
