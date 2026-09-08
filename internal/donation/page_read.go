package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (s *Service) beginBrowseTx(ctx context.Context, role reviewerRole, userID int64) (*sql.Tx, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	switch role {
	case recurringOwner:
		if nilDependency(s.ownerAuth) || userID <= 0 {
			err = ErrUnauthorized
		} else {
			err = mapAuthorization(s.ownerAuth.AuthorizeUserMutation(ctx, tx, userID))
		}
	case reviewerAdmin:
		if nilDependency(s.roleAuth) {
			err = ErrUnavailable
		} else {
			err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&userID)
			if errors.Is(err, sql.ErrNoRows) {
				err = ErrForbidden
			}
			if err == nil {
				err = mapAuthorization(s.roleAuth.AuthorizeAdminMutation(ctx, tx, userID))
			}
		}
	case reviewerSteward:
		if nilDependency(s.roleAuth) || userID <= 0 {
			err = ErrForbidden
		} else {
			err = mapAuthorization(s.roleAuth.AuthorizeStewardMutation(ctx, tx, userID))
		}
	default:
		err = ErrInvalidRequest
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func browseIDs(ctx context.Context, tx *sql.Tx, query string, args []any, page pagination.Request) ([]int64, pagination.Metadata, error) {
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+query+`)`, args...).Scan(&total); err != nil {
		return nil, pagination.Metadata{}, fmt.Errorf("donation: count browse rows: %w", err)
	}
	metadata, offset, err := page.Window(total)
	if err != nil {
		return nil, metadata, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, query+` ORDER BY id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return nil, metadata, fmt.Errorf("donation: read browse rows: %w", err)
	}
	ids, err := scanIDs(rows)
	return ids, metadata, err
}

// Logical expiration affects browse state immediately. Its cleanup event has
// no recorded timestamp yet, so it must not invent one or advance a revision.
// The eventual mutation/worker records the actual event using the normal path.
func logicalHeaderTx(ctx context.Context, tx *sql.Tx, donationID, now int64) (AdminDonation, error) {
	out, err := getDonationHeaderTx(ctx, tx, donationID)
	if err != nil {
		return out, err
	}
	if out.Status == "pending" || out.Status == "approved" {
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT `+logicallyActiveDonationSQL+` FROM donations d WHERE d.id=?`, now, donationID).Scan(&active)
		if err != nil {
			return out, err
		}
		if !active {
			out.Status = "expired"
			if out.Handling.State == "pending" {
				out.Handling.State = "closed"
				reason := "expired"
				out.Handling.ClosedReason = &reason
			}
		}
	}
	return out, nil
}

func recurringSummaryTx(ctx context.Context, tx *sql.Tx, keyID, now int64) (RecurringSummary, error) {
	rules, err := donationquota.Views(ctx, tx, keyID, now)
	if err != nil {
		return RecurringSummary{}, quotaError(err)
	}
	count := strconv.Itoa(len(rules))
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].State == "limited" && rules[j].State != "limited" })
	if len(rules) > 3 {
		rules = rules[:3]
	}
	return RecurringSummary{RuleCount: count, Rules: rules}, nil
}

func managedKeySummary(key AdminDonationKey, header AdminDonation, rules RecurringSummary) ManagedKeySummary {
	return ManagedKeySummary{DonationKey: ownerKey(key), RecurringReceipt: RecurringReceipt{
		DonationID: header.ID, KeyID: key.ID, DonationRevision: header.Revision}, RecurringSummary: rules,
		AuthorizedExpiresAt: key.AuthorizedExpiresAt, SafeNote: key.SafeNote,
		MaxConcurrency: key.MaxConcurrency, MaxRPM: key.MaxRPM, BindingCount: key.BindingCount, Idle: key.Idle, Handling: header.Handling}
}

func (s *Service) keysPage(ctx context.Context, role reviewerRole, userID, donationID int64, page pagination.Request) (Page[ManagedKeySummary], error) {
	var empty Page[ManagedKeySummary]
	if ctx == nil || donationID <= 0 || !page.Valid() {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginBrowseTx(ctx, role, userID)
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
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations WHERE id=? AND user_id=?)`, donationID, userID).Scan(&owned); err != nil {
			return empty, err
		}
		if !owned {
			return empty, ErrNotFound
		}
	}
	visible, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
	if err != nil {
		return empty, err
	}
	if !visible && role == reviewerAdmin && s.heldRead != nil {
		visible, err = s.heldRead.AuthorizeHeldDonationRead(ctx, tx, donationID, now)
		if err != nil {
			return empty, err
		}
	}
	if !visible {
		return empty, ErrNotFound
	}
	header, err := logicalHeaderTx(ctx, tx, donationID, now)
	if err != nil {
		return empty, err
	}
	ids, metadata, err := browseIDs(ctx, tx, `SELECT id FROM donation_keys WHERE donation_id=?`, []any{donationID}, page)
	if err != nil {
		return empty, err
	}
	keys, err := readDonationKeySelectionTx(ctx, tx, donationID, header.Status, now, ids)
	if err != nil {
		return empty, err
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
		result.Data = append(result.Data, managedKeySummary(key, header, rules))
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) KeysOwnerPage(ctx context.Context, userID, donationID int64, page pagination.Request) (Page[OwnerKeySummary], error) {
	result, err := s.keysPage(ctx, recurringOwner, userID, donationID, page)
	if err != nil {
		return Page[OwnerKeySummary]{}, err
	}
	out := Page[OwnerKeySummary]{Data: make([]OwnerKeySummary, 0, len(result.Data)), Pagination: result.Pagination}
	for _, key := range result.Data {
		out.Data = append(out.Data, OwnerKeySummary{DonationKey: key.DonationKey, RecurringReceipt: key.RecurringReceipt, RecurringSummary: key.RecurringSummary})
	}
	return out, nil
}

func (s *Service) KeysAdminPage(ctx context.Context, donationID int64, page pagination.Request) (Page[ManagedKeySummary], error) {
	return s.keysPage(ctx, reviewerAdmin, 0, donationID, page)
}

func (s *Service) KeysStewardPage(ctx context.Context, userID, donationID int64, page pagination.Request) (Page[ManagedKeySummary], error) {
	return s.keysPage(ctx, reviewerSteward, userID, donationID, page)
}
