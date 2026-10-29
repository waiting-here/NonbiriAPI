package donation

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// AutomationDonation excludes account profiles and private resource notes.
type AutomationDonation struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Revision    string `json:"revision"`
	Description string `json:"description"`
	KeyCount    string `json:"key_count"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}
type AutomationPage[T any] struct {
	Data       []T                  `json:"data"`
	Pagination *pagination.Metadata `json:"pagination"`
}

func automationDonation(ctx context.Context, tx *sql.Tx, id, now int64) (AutomationDonation, error) {
	header, err := logicalHeaderTx(ctx, tx, id, now)
	if err != nil {
		return AutomationDonation{}, err
	}
	var count int64
	query := `SELECT COUNT(*) FROM donation_keys dk WHERE donation_id=?`

	err = tx.QueryRowContext(ctx, query, id).Scan(&count)
	return AutomationDonation{ID: header.ID, Status: header.Status, Revision: header.Revision, Description: header.Description, KeyCount: strconv.FormatInt(count, 10), CreatedAt: header.CreatedAt, UpdatedAt: header.UpdatedAt}, err
}
func (s *Service) AutomationDonations(ctx context.Context, userID int64, q string, page pagination.Request) (AutomationPage[AutomationDonation], error) {
	out := AutomationPage[AutomationDonation]{Data: []AutomationDonation{}}
	tx, err := s.beginDonationPageTx(ctx, reviewerSteward, userID)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return out, err
	}
	selection := `SELECT d.id FROM donations d WHERE (d.status IN ('pending','approved') OR d.terminal_at>?)`
	args := []any{now - terminalRetention}
	access, err := readDonationAccessScope(ctx, tx, reviewerSteward, userID)
	if err != nil {
		return out, err
	}
	predicate, values := access.predicate(now)
	selection += " AND " + predicate
	args = append(args, values...)

	if q != "" {
		selection += ` AND (d.description LIKE ? ESCAPE '\' OR EXISTS(SELECT 1 FROM donation_keys qk WHERE qk.donation_id=d.id` + ` AND (qk.safe_note LIKE ? ESCAPE '\' OR qk.canonical_base_url LIKE ? ESCAPE '\' OR qk.connector_type LIKE ? ESCAPE '\')))`
		pattern := "%" + escapeAutomationLike(q) + "%"
		args = append(args, pattern, pattern, pattern, pattern)
	}
	ids, meta, err := browseIDs(ctx, tx, selection, args, page)
	if err != nil {
		return out, err
	}
	out.Pagination = &meta
	for _, id := range ids {
		value, err := automationDonation(ctx, tx, id, now)
		if err != nil {
			return out, err
		}
		out.Data = append(out.Data, value)
	}
	return out, tx.Commit()
}
func escapeAutomationLike(value string) string {
	out := ""
	for _, r := range value {
		if r == '\\' || r == '%' || r == '_' {
			out += "\\"
		}
		out += string(r)
	}
	return out
}
func (s *Service) automationDonationTx(ctx context.Context, userID, donationID int64) (*sql.Tx, int64, error) {
	tx, _, _, err := s.beginReviewTx(ctx, reviewerSteward, userID, true)
	if err != nil {
		return nil, 0, err
	}
	now, err := s.nowUnix()
	if err == nil {
		var visible bool
		visible, err = donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
		if err == nil && !visible {
			visible, err = s.managementHeldRead(ctx, tx, reviewerSteward, userID, donationID, now)
		}
		if err == nil && !visible {
			err = ErrNotFound
		}
	}
	if err == nil {
		err = requireDonationRangeTx(ctx, tx, reviewerSteward, userID, donationID, now)
	}
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	return tx, now, nil
}
func (s *Service) AutomationDonation(ctx context.Context, userID, id int64) (AutomationDonation, error) {
	tx, now, err := s.automationDonationTx(ctx, userID, id)
	if err != nil {
		return AutomationDonation{}, err
	}
	defer tx.Rollback()
	out, err := automationDonation(ctx, tx, id, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) AutomationKeys(ctx context.Context, userID, donationID int64, q string, page pagination.Request) (AutomationPage[AdminDonationKey], error) {
	out := AutomationPage[AdminDonationKey]{Data: []AdminDonationKey{}}
	tx, now, err := s.automationDonationTx(ctx, userID, donationID)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	header, err := logicalHeaderTx(ctx, tx, donationID, now)
	if err != nil {
		return out, err
	}
	selection := `SELECT dk.id FROM donation_keys dk WHERE dk.donation_id=?`
	args := []any{donationID}
	access, err := readDonationAccessScope(ctx, tx, reviewerSteward, userID)
	if err != nil {
		return out, err
	}

	if q != "" {
		selection += ` AND (dk.safe_note LIKE ? ESCAPE '\' OR dk.canonical_base_url LIKE ? ESCAPE '\' OR dk.connector_type LIKE ? ESCAPE '\')`
		pattern := "%" + escapeAutomationLike(q) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	ids, meta, err := browseIDs(ctx, tx, selection, args, page)
	if err != nil {
		return out, err
	}
	out.Pagination = &meta
	out.Data, err = readDonationKeySelectionWithStatusesTx(ctx, tx, donationID, header.Status, now, ids)
	if err != nil {
		return out, err
	}
	for i := range out.Data {
		if access.redact {
			out.Data[i].EndpointKeyID = nil
			out.Data[i].OwnerEndpointID = nil
		}
		if !access.manage {
			out.Data[i].CanManage = false
		}
	}
	return out, tx.Commit()
}
func (s *Service) AutomationKey(ctx context.Context, userID, donationID, keyID int64) (AdminDonationKey, error) {
	tx, now, err := s.automationDonationTx(ctx, userID, donationID)
	if err != nil {
		return AdminDonationKey{}, err
	}
	defer tx.Rollback()
	header, err := logicalHeaderTx(ctx, tx, donationID, now)
	if err != nil {
		return AdminDonationKey{}, err
	}
	access, err := readDonationAccessScope(ctx, tx, reviewerSteward, userID)
	if err != nil {
		return AdminDonationKey{}, err
	}
	keys, err := readDonationKeySelectionWithStatusesTx(ctx, tx, donationID, header.Status, now, []int64{keyID})
	if err != nil {
		return AdminDonationKey{}, err
	}
	if len(keys) != 1 {
		return AdminDonationKey{}, ErrNotFound
	}
	if access.redact {
		keys[0].EndpointKeyID = nil
		keys[0].OwnerEndpointID = nil
	}
	if !access.manage {
		keys[0].CanManage = false
	}
	return keys[0], tx.Commit()
}
