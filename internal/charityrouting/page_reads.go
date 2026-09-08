package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Page counts and rows share the authorized snapshot. Browse projections use
// logical expiry predicates without materializing events during a read.
func (s *Service) beginPageRead(ctx context.Context, role roleKind, actorID int64) (*sql.Tx, error) {
	if s == nil || s.db == nil || nilDependency(s.roleAuth) {
		return nil, ErrUnavailable
	}
	if role != roleAdmin && role != roleSteward || role == roleSteward && actorID <= 0 {
		return nil, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if role == roleAdmin {
		err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&actorID)
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrForbidden
		}
		if err == nil {
			err = mapAuthorization(s.roleAuth.AuthorizeAdminMutation(ctx, tx, actorID))
		}
	} else {
		err = mapAuthorization(s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorID))
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func pageWindow(ctx context.Context, tx *sql.Tx, selection string, args []any, page pagination.Request) (pagination.Metadata, int64, error) {
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+selection+`)`, args...).Scan(&total); err != nil {
		return pagination.Metadata{}, 0, err
	}
	metadata, offset, err := page.Window(total)
	if err != nil {
		return metadata, 0, ErrInvalidRequest
	}
	return metadata, offset, nil
}

func (s *Service) modelsPage(ctx context.Context, role roleKind, actorID int64, query string, enabled *bool, page pagination.Request) (Page[AdminCharityModel], error) {
	var empty Page[AdminCharityModel]
	if ctx == nil || !page.Valid() || !utf8.ValidString(query) || utf8.RuneCountInString(query) > 128 {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	selection := `SELECT id FROM charity_models WHERE 1=1`
	args := []any{}
	if query != "" {
		selection += ` AND (provider LIKE ? ESCAPE '\' OR model LIKE ? ESCAPE '\' OR full_name LIKE ? ESCAPE '\')`
		pattern := "%" + escapeLike(query) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	if enabled != nil {
		selection += ` AND enabled=?`
		args = append(args, boolInt(*enabled))
	}
	metadata, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return empty, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return empty, err
	}
	result := Page[AdminCharityModel]{Data: make([]AdminCharityModel, 0, len(ids)), Pagination: &metadata}
	for _, id := range ids {
		item, err := getAdminModelTx(ctx, tx, id)
		if err != nil {
			return empty, err
		}
		result.Data = append(result.Data, item)
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

const candidatePageFrom = ` FROM charity_models cm
CROSS JOIN donation_keys dk NOT INDEXED
CROSS JOIN donations d ON d.id=dk.donation_id
CROSS JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
CROSS JOIN endpoint_keys k ON k.id=m.endpoint_key_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id
CROSS JOIN model_pair_catalog pc ON pc.endpoint_key_id=k.id
WHERE cm.id=? AND d.status='approved' AND d.user_id IS NOT NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND NOT EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.charity_model_id=cm.id
 AND b.donation_key_id=dk.id AND b.upstream_model_id=pc.normalized_model_id)`

func candidatePageSelection(modelID, now int64, query CandidateQuery) (string, []any) {
	statement := `SELECT dk.id,d.id,dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,
pc.normalized_model_id,pc.automatic_supports,pc.manual_supports,COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0)` + candidatePageFrom
	args := []any{modelID, now}
	if query.DonationID > 0 {
		statement += ` AND d.id=?`
		args = append(args, query.DonationID)
	}
	if query.DonationKeyID > 0 {
		statement += ` AND dk.id=?`
		args = append(args, query.DonationKeyID)
	}
	if query.Source == "automatic" {
		statement += ` AND pc.automatic_supports>0`
	} else if query.Source == "manual" {
		statement += ` AND pc.manual_supports>0`
	} else {
		statement += ` AND (pc.automatic_supports>0 OR pc.manual_supports>0)`
	}
	if query.Query != "" {
		statement += ` AND pc.normalized_model_id LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(query.Query)+"%")
	}
	return statement, args
}

func (s *Service) candidatesPage(ctx context.Context, role roleKind, actorID, modelID int64, query CandidateQuery, page pagination.Request) (Page[AdminBindingCandidate], error) {
	var empty Page[AdminBindingCandidate]
	if ctx == nil || modelID <= 0 || !page.Valid() || query.DonationID < 0 || query.DonationKeyID < 0 ||
		query.AfterKeyID != 0 || query.AfterModelID != "" || query.Source != "" && query.Source != "automatic" && query.Source != "manual" ||
		!utf8.ValidString(query.Query) || utf8.RuneCountInString(query.Query) > 512 {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, err := getAdminModelTx(ctx, tx, modelID); err != nil {
		return empty, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	selection, args := candidatePageSelection(modelID, now, query)
	metadata, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return empty, err
	}
	rows, err := tx.QueryContext(ctx, selection+` ORDER BY dk.id,pc.normalized_model_id LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return empty, err
	}
	defer rows.Close()
	result := Page[AdminBindingCandidate]{Data: make([]AdminBindingCandidate, 0, page.Size), Pagination: &metadata}
	for rows.Next() {
		var item AdminBindingCandidate
		var keyID, donationID, automatic, manual int64
		if err := rows.Scan(&keyID, &donationID, &item.Source.ConnectorType, &item.Source.CanonicalBaseURL,
			&item.Source.DisplayHead, &item.Source.DisplayTail, &item.UpstreamModelID, &automatic, &manual, &item.Source.MaxConcurrency, &item.Source.MaxRPM); err != nil {
			return empty, err
		}
		item.DonationKeyID = strconv.FormatInt(keyID, 10)
		item.DonationID = strconv.FormatInt(donationID, 10)
		item.SourceTypes = sourceTypes(automatic, manual)
		result.Data = append(result.Data, item)
	}
	if err := rows.Err(); err != nil {
		return empty, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) bindingSourcesPage(ctx context.Context, role roleKind, actorID, modelID, donationID int64, page pagination.Request) (Page[BindingDonation], Page[BindingSourceKey], error) {
	var donations Page[BindingDonation]
	var keys Page[BindingSourceKey]
	if ctx == nil || modelID <= 0 || donationID < 0 || !page.Valid() {
		return donations, keys, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, role, actorID)
	if err != nil {
		return donations, keys, err
	}
	defer tx.Rollback()
	if _, err := getAdminModelTx(ctx, tx, modelID); err != nil {
		return donations, keys, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return donations, keys, err
	}
	selection := `SELECT d.id,d.description,COUNT(DISTINCT dk.id)` + bindingSourceFrom + ` GROUP BY d.id,d.description`
	order := ` ORDER BY d.id`
	args := []any{modelID, now}
	if donationID > 0 {
		selection = `SELECT dk.id,dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,dk.safe_note,
COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0)` + bindingSourceFrom + ` AND d.id=?`
		args = append(args, donationID)
		order = ` ORDER BY dk.id`
	}
	metadata, offset, err := pageWindow(ctx, tx, selection, args, page)
	if err != nil {
		return donations, keys, err
	}
	rows, err := tx.QueryContext(ctx, selection+order+` LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return donations, keys, err
	}
	defer rows.Close()
	donations = Page[BindingDonation]{Data: make([]BindingDonation, 0, page.Size), Pagination: &metadata}
	keys = Page[BindingSourceKey]{Data: make([]BindingSourceKey, 0, page.Size), Pagination: &metadata}
	for rows.Next() {
		var id int64
		if donationID == 0 {
			var item BindingDonation
			if err := rows.Scan(&id, &item.Description, &item.KeyCount); err != nil {
				return donations, keys, err
			}
			item.ID = strconv.FormatInt(id, 10)
			donations.Data = append(donations.Data, item)
		} else {
			var item BindingSourceKey
			if err := rows.Scan(&id, &item.Source.ConnectorType, &item.Source.CanonicalBaseURL, &item.Source.DisplayHead, &item.Source.DisplayTail, &item.Note, &item.Source.MaxConcurrency, &item.Source.MaxRPM); err != nil {
				return donations, keys, err
			}
			item.DonationKeyID = strconv.FormatInt(id, 10)
			keys.Data = append(keys.Data, item)
		}
	}
	if err := rows.Err(); err != nil {
		return donations, keys, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return donations, keys, err
	}
	return donations, keys, nil
}
