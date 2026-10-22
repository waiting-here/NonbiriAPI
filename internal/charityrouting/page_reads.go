package charityrouting

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// Page counts and rows share the authorized snapshot. Browse projections use
// logical expiry predicates without materializing events during a read.
func (s *Service) beginPageRead(ctx context.Context, role roleKind, actorID int64) (*sql.Tx, error) {
	tx, _, err := s.beginManagementTx(ctx, role, actorID, 0, true, true)
	return tx, err
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
	scope, err := s.managementScope(ctx, tx, role, actorID, 0, true)
	if err != nil {
		return empty, err
	}
	selection := `SELECT id FROM charity_models WHERE 1=1`
	if scope.Trainee {
		selection += ` AND is_mainstream=1`
	}
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
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	result := Page[AdminCharityModel]{Data: make([]AdminCharityModel, 0, len(ids)), Pagination: &metadata}
	for _, id := range ids {
		item, err := getAdminModelTx(ctx, tx, id, now)
		if err != nil {
			return empty, err
		}
		result.Data = append(result.Data, item)
	}
	s.attachModelCloudflareMode(result.Data)
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

// Probe the approved member's runs directly. Materializing the aggregate catalog
// view here would collect other members' catalogs before pagination or filtering.
const candidateApprovedRuns = `SELECT r.id FROM donation_uses u
JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id AND ur.review_state='approved'
JOIN discovery_source_states st ON st.source_revision_id=ur.discovery_source_revision_id
JOIN discovery_runs r ON r.id=st.last_successful_run_id AND r.source_revision_id=ur.discovery_source_revision_id AND r.state='succeeded'
WHERE u.donation_key_id=dk.id`

const candidateAutomaticJoin = `CROSS JOIN model_catalog_entries pc INDEXED BY idx_catalog_run_model
ON pc.discovery_run_id IN (` + candidateApprovedRuns + `) AND pc.source_type='automatic'`

const candidateMatchingAutomatic = ` FROM model_catalog_entries ac INDEXED BY idx_catalog_run_model
WHERE ac.discovery_run_id IN (` + candidateApprovedRuns + `)
AND ac.normalized_model_id=pc.normalized_model_id AND ac.source_type='automatic'`

const candidatePageFrom = ` FROM charity_models cm
CROSS JOIN donation_keys dk NOT INDEXED
CROSS JOIN donations d ON d.id=dk.donation_id
CROSS JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
CROSS JOIN endpoint_keys k ON k.id=m.endpoint_key_id
LEFT JOIN endpoint_key_limits kl ON kl.endpoint_key_id=k.id
` + candidateAutomaticJoin + `
WHERE cm.id=? AND d.status IN ('pending','approved') AND d.terminal_at IS NULL AND d.user_id IS NOT NULL
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
AND EXISTS(SELECT 1 FROM donation_uses u WHERE u.donation_key_id=dk.id AND u.current_approved_revision_id IS NOT NULL)
` + candidateUnboundPredicate

const candidateUnboundPredicate = `AND NOT EXISTS(SELECT 1 FROM charity_model_bindings b JOIN charity_binding_targets bt ON bt.binding_id=b.id WHERE b.charity_model_id=cm.id
 AND bt.donation_key_id=dk.id AND bt.upstream_model_id=pc.normalized_model_id)`

func candidatePageSelection(modelID, now int64, query CandidateQuery) (string, []any) {
	statement := `SELECT DISTINCT dk.id,d.id,dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,
pc.normalized_model_id,pc.automatic_supports,pc.manual_supports,COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0)` + candidatePageFrom
	if query.IncludeBound {
		statement = strings.Replace(statement, candidateUnboundPredicate, "", 1)
	}
	args := []any{modelID, now}
	if donationstate.Filtered(query.KeyState) {
		// Evaluate key state before expanding its model pairs.
		statement = strings.Replace(statement, "CROSS JOIN endpoint_keys k ON k.id=m.endpoint_key_id",
			"CROSS JOIN endpoint_keys k ON k.id=m.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id", 1)
		statement += ` AND (` + donationstate.SQL() + `)=?`
		args = append(args, now, now, now, now, query.KeyState)
	}
	if query.traineeOnly {
		statement += ` AND dk.mainstream_channel_id IS NOT NULL`
	}
	if query.DonationID > 0 {
		statement += ` AND d.id=?`
		args = append(args, query.DonationID)
	}
	if query.DonationKeyID > 0 {
		statement += ` AND dk.id=?`
		args = append(args, query.DonationKeyID)
	}
	if query.Query != "" {
		statement += ` AND pc.normalized_model_id LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(query.Query)+"%")
	}
	if query.AfterKeyID > 0 {
		statement += ` AND (dk.id>? OR (dk.id=? AND pc.normalized_model_id>?))`
		args = append(args, query.AfterKeyID, query.AfterKeyID, query.AfterModelID)
	}
	manual := strings.Replace(statement, candidateAutomaticJoin, "CROSS JOIN donation_key_manual_models pc ON pc.donation_key_id=dk.id", 1)
	manual = strings.ReplaceAll(manual, "pc.automatic_supports", "0")
	manual = strings.ReplaceAll(manual, "pc.manual_supports", "1")
	manual += ` AND NOT EXISTS(SELECT 1` + candidateMatchingAutomatic + `)`
	// Each branch already proves its own source exists.
	if query.Source == "automatic" {
		manual += ` AND 0`
	} else if query.Source == "manual" {
		statement += ` AND pc.manual_supports>0`
	}
	statement = strings.ReplaceAll(statement, "pc.automatic_supports", "1")
	statement = strings.ReplaceAll(statement, "pc.manual_supports", `EXISTS(SELECT 1 FROM donation_key_manual_models dm WHERE dm.donation_key_id=dk.id AND dm.normalized_model_id=pc.normalized_model_id)`)
	// Callers need source presence, not entry counts. DISTINCT collapses the
	// same key/model across runs without a correlated COUNT and MIN per entry.
	return statement + ` UNION ALL ` + manual, append(append([]any{}, args...), args...)
}

func (s *Service) candidatesPage(ctx context.Context, role roleKind, actorID, modelID int64, query CandidateQuery, page pagination.Request) (Page[AdminBindingCandidate], error) {
	var empty Page[AdminBindingCandidate]
	if ctx == nil || modelID <= 0 || !page.Valid() || query.DonationID < 0 || query.DonationKeyID < 0 ||
		query.AfterKeyID != 0 || query.AfterModelID != "" || query.Source != "" && query.Source != "automatic" && query.Source != "manual" ||
		!utf8.ValidString(query.Query) || utf8.RuneCountInString(query.Query) > 512 || !donationstate.Valid(query.KeyState) {
		return empty, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, role, actorID)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	scope, err := s.managementScope(ctx, tx, role, actorID, modelID, false)
	if err != nil {
		return empty, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	query.traineeOnly = scope.Trainee
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
	if err := annotateCandidateServicesTx(ctx, tx, result.Data, modelID); err != nil {
		return empty, err
	}
	if !query.omitBindingMarkers {
		if err := annotateBindingCandidates(ctx, tx, result.Data, modelID, scope.Trainee); err != nil {
			return empty, err
		}
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) bindingSourcesPage(ctx context.Context, role roleKind, actorID, modelID, donationID int64, keyState string, page pagination.Request, includeBound ...bool) (Page[BindingDonation], Page[BindingSourceKey], error) {
	var donations Page[BindingDonation]
	var keys Page[BindingSourceKey]
	if ctx == nil || modelID <= 0 || donationID < 0 || !page.Valid() || !donationstate.Valid(keyState) {
		return donations, keys, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.beginPageRead(ctx, role, actorID)
	if err != nil {
		return donations, keys, err
	}
	defer tx.Rollback()
	scope, err := s.managementScope(ctx, tx, role, actorID, modelID, false)
	if err != nil {
		return donations, keys, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return donations, keys, err
	}
	from, args := bindingSourceSelection(modelID, now, keyState, includeBound...)
	if scope.Trainee {
		from += ` AND dk.mainstream_channel_id IS NOT NULL`
	}
	selection := `SELECT d.id,d.description,COUNT(DISTINCT dk.id)` + from + ` GROUP BY d.id,d.description`
	order := ` ORDER BY d.id`
	if donationID > 0 {
		selection = `SELECT dk.id,dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,dk.safe_note,
COALESCE(kl.max_concurrency,0),COALESCE(kl.max_rpm,0),d.description,d.review_note` + from + ` AND d.id=?`
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
			if err := rows.Scan(&id, &item.Source.ConnectorType, &item.Source.CanonicalBaseURL, &item.Source.DisplayHead, &item.Source.DisplayTail, &item.Note, &item.Source.MaxConcurrency, &item.Source.MaxRPM, &item.DonationNote, &item.ApprovalNote); err != nil {
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
