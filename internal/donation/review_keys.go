package donation

import (
	"context"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// A review read without a selected charity model never grants key maintenance.
// It exposes the same pending key subset as the existing reviewer detail.
func (s *Service) reviewKeysPage(ctx context.Context, userID, donationID int64, page pagination.Request) (Page[ManagedKeySummary], error) {
	var empty Page[ManagedKeySummary]
	tx, role, _, err := s.beginReviewTx(ctx, reviewerSteward, userID, true)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if role == reviewerTrainee {
		if err := authz.RequireStewardPermission(ctx, tx, userID, "donations.review"); err != nil {
			return empty, mapAuthorization(err)
		}
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	visible, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
	if err != nil {
		return empty, err
	}
	if !visible {
		return empty, ErrNotFound
	}
	if role == reviewerTrainee {
		if err := requirePendingUseReviewTx(ctx, tx, donationID, now); err != nil {
			return empty, err
		}
	}
	header, err := logicalHeaderTx(ctx, tx, donationID, now)
	if err != nil {
		return empty, err
	}
	query := `SELECT dk.id FROM donation_keys dk WHERE dk.donation_id=?`
	if role == reviewerTrainee {
		query += ` AND EXISTS(SELECT 1 FROM donation_uses u WHERE u.donation_key_id=dk.id AND u.current_pending_revision_id IS NOT NULL)`
	}
	ids, metadata, err := browseIDs(ctx, tx, query, []any{donationID}, page)
	if err != nil {
		return empty, err
	}
	keys, err := readDonationKeySelectionTx(ctx, tx, donationID, header.Status, now, ids)
	if err != nil {
		return empty, err
	}
	if role == reviewerTrainee {
		header = traineeReviewProjection(header)
	}
	result := Page[ManagedKeySummary]{Data: make([]ManagedKeySummary, 0, len(keys)), Pagination: &metadata}
	for _, key := range keys {
		id, err := strconv.ParseInt(key.ID, 10, 64)
		if err != nil {
			return empty, ErrInvariant
		}
		rules, err := recurringSummaryTx(ctx, tx, id, now)
		if err != nil {
			return empty, err
		}
		value := managedKeySummary(key, header, rules)
		value.CanManage = false
		value.MaintenanceScopeRequired = true
		if role == reviewerTrainee {
			value.EndpointKeyID = nil
		}
		result.Data = append(result.Data, value)
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
