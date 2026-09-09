package donation

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const sourceIdentitySQL = `nbi_donation_source(dk.mainstream_channel_id,dk.connector_type,dk.canonical_base_url)`

const sourceChannelSQL = `COALESCE(dk.mainstream_channel_id,'')`
const sourceConnectorSQL = `CASE WHEN dk.mainstream_channel_id IS NULL THEN dk.connector_type ELSE '' END`
const sourceURLSQL = `CASE WHEN dk.mainstream_channel_id IS NULL THEN dk.canonical_base_url ELSE '' END`
const sourceGroupColumns = `source_channel,source_connector,source_url`

type sourceIdentity struct{ channel, connector, url string }

func validSourceFilter(filter SourceFilter, keys bool) bool {
	return (filter.Scope == "" || filter.Scope == "active" || filter.Scope == "all") &&
		(filter.Handling == "" || filter.Handling == "pending") &&
		(filter.Idle == "" || keys && (filter.Idle == "yes" || filter.Idle == "no")) &&
		utf8.ValidString(filter.Query) && len(filter.Query) <= 512 && utf8.RuneCountInString(filter.Query) <= 128 && !strings.ContainsRune(filter.Query, 0)
}

// This query contains only management-visible source/key metadata. It never
// joins private endpoint notes or donor identities for filtering or counting.
func sourceSelectionSQL(filter SourceFilter, source *sourceIdentity, now int64) (string, []any) {
	query := `SELECT dk.id AS id,d.id AS donation_id,d.status AS donation_status,` + sourceChannelSQL + ` AS source_channel,` + sourceConnectorSQL + ` AS source_connector,` + sourceURLSQL + ` AS source_url,
CASE WHEN h.state='pending' AND ` + logicallyActiveDonationSQL + ` THEN 1 ELSE 0 END AS pending
FROM donation_keys dk INDEXED BY idx_donation_keys_source_page CROSS JOIN donations d ON d.id=dk.donation_id
JOIN donation_handling h ON h.donation_id=d.id
WHERE (d.status IN ('pending','approved') OR d.terminal_at>?)`
	args := []any{now, now - terminalRetention}
	if filter.Scope != "all" {
		query += ` AND ` + logicallyActiveDonationSQL
		args = append(args, now)
	}
	if filter.Handling == "pending" {
		query += ` AND h.state='pending' AND ` + logicallyActiveDonationSQL
		args = append(args, now)
	}
	if source != nil {
		query += ` AND ` + sourceChannelSQL + `=? AND ` + sourceConnectorSQL + `=? AND ` + sourceURLSQL + `=?`
		args = append(args, source.channel, source.connector, source.url)
	}
	if filter.Idle != "" {
		query += ` AND `
		if filter.Idle == "yes" {
			query += `NOT `
		}
		query += `EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.donation_key_id=dk.id)`
	}
	if filter.Query != "" {
		query += ` AND (instr(lower(dk.canonical_base_url),lower(?))>0
 OR instr(lower(dk.connector_type),lower(?))>0 OR instr(lower(COALESCE(dk.mainstream_channel_name,'')),lower(?))>0
 OR instr(lower(d.description),lower(?))>0 OR instr(lower(dk.safe_note),lower(?))>0
 OR instr(lower(dk.display_head),lower(?))>0 OR instr(lower(dk.display_tail),lower(?))>0
 OR instr(CAST(d.id AS TEXT),?)>0 OR instr(CAST(dk.id AS TEXT),?)>0)`
		for range 9 {
			args = append(args, filter.Query)
		}
	}
	return query, args
}

// All counters are wide binary magnitudes. The minimum undiscounted reserve
// across eligible bindings is sufficient because every other key requirement
// is independent of the model. Caller balances and personal guards are absent.
func sourceUsableSQL() string {
	return `WITH key_cost AS (
 SELECT dk.*,d.status AS donation_status,d.user_id,
 COALESCE(e.enabled,0) AS endpoint_enabled,COALESCE(k.enabled,0) AS physical_enabled,
 EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id AND m.endpoint_key_id=k.id) AS member,
 EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id) AS suspended,
 (SELECT MIN(CASE cm.pricing_mode WHEN 'per_request' THEN cm.request_user_price
 WHEN 'per_token' THEN CAST((SELECT value FROM site_config WHERE key='charity_token_reserve_milli') AS INTEGER) END)
 FROM charity_model_bindings b JOIN charity_models cm ON cm.id=b.charity_model_id
JOIN charity_model_access a ON a.model_id=cm.id
 JOIN model_pair_catalog pc ON pc.endpoint_key_id=b.endpoint_key_id AND pc.normalized_model_id=b.upstream_model_id
 WHERE b.donation_key_id=dk.id AND b.endpoint_key_id=k.id AND cm.enabled=1 AND a.allowed_level_mask<>0
 AND (pc.automatic_supports>0 OR pc.manual_supports>0)
 AND e.connector_type IN ('openai-compatible','anthropic-compatible')) AS price_reserve
 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
 LEFT JOIN endpoint_keys k ON k.id=dk.endpoint_key_id LEFT JOIN endpoints e ON e.id=k.endpoint_id
 WHERE dk.id=?
)
SELECT CASE WHEN dk.donation_status='approved' AND dk.user_id IS NOT NULL AND dk.ended_at IS NULL
 AND dk.enabled=1 AND dk.failure_disabled=0 AND dk.member=1 AND dk.suspended=0
 AND dk.endpoint_enabled=1 AND dk.physical_enabled=1 AND (dk.expires_at IS NULL OR dk.expires_at>?)
 AND dk.price_reserve IS NOT NULL AND (SELECT value FROM site_config WHERE key='charity_enabled')='1'
 THEN CASE WHEN
 nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))>nbi_u128(0)
 AND nbi_u128_remaining(dk.price_limit_mag,dk.price_used_mag,dk.price_reserved_mag,nbi_u128(0))>=nbi_u128(dk.price_reserve)
 AND nbi_u128_remaining(dk.call_limit_mag,dk.calls_used,dk.calls_reserved,nbi_u128(0))>=nbi_u128(1)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))>nbi_u128(0)
 AND nbi_u128_remaining(dk.token_limit_mag,dk.tokens_used,dk.tokens_reserved,nbi_u128(0))>=nbi_u128(dk.token_reserve)
 AND ` + donationquota.AvailabilityPredicate("dk.id", "?", "dk.price_reserve", "dk.token_reserve") + `
 THEN 1 ELSE 0 END ELSE 0 END FROM key_cost dk`
}

func sourcePreviewTx(ctx context.Context, tx *sql.Tx, keyID int64) (SafeSource, error) {
	var out SafeSource
	err := tx.QueryRowContext(ctx, `SELECT connector_type,canonical_base_url,mainstream_channel_id,mainstream_channel_name FROM donation_keys WHERE id=?`, keyID).Scan(&out.ConnectorType, &out.BaseURL, &out.ChannelID, &out.Name)
	if err != nil {
		return out, err
	}
	out.Kind = "custom"
	if out.ChannelID != nil {
		out.Kind = "mainstream"
	}
	return out, nil
}

func (s *Service) sourcesPage(ctx context.Context, role reviewerRole, userID int64, filter SourceFilter, page pagination.Request) (Page[DonationSource], error) {
	var empty Page[DonationSource]
	if ctx == nil || !page.Valid() || !validSourceFilter(filter, false) {
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
	selection, args := sourceSelectionSQL(filter, nil, now)
	grouped := `SELECT ` + sourceGroupColumns + `,COUNT(DISTINCT donation_id),COUNT(*),COUNT(DISTINCT CASE WHEN pending=1 THEN donation_id END),MAX(id),MAX(CASE WHEN donation_status='approved' THEN 1 ELSE 0 END) FROM (` + selection + `) GROUP BY ` + sourceGroupColumns
	var total int64
	groupCount := `SELECT COUNT(*) FROM (SELECT ` + sourceGroupColumns + ` FROM (` + selection + `) GROUP BY ` + sourceGroupColumns + `)`
	if err := tx.QueryRowContext(ctx, groupCount, args...).Scan(&total); err != nil {
		return empty, fmt.Errorf("donation: count sources: %w", err)
	}
	metadata, offset, err := page.Window(total)
	if err != nil {
		return empty, ErrInvalidRequest
	}
	rows, err := tx.QueryContext(ctx, grouped+` ORDER BY `+sourceGroupColumns+` LIMIT ? OFFSET ?`, append(args, page.Size, offset)...)
	if err != nil {
		return empty, err
	}
	result := Page[DonationSource]{Data: make([]DonationSource, 0), Pagination: &metadata}
	previews := make([]int64, 0)
	identities := make([]sourceIdentity, 0)
	usableCandidates := make([]bool, 0)
	for rows.Next() {
		var item DonationSource
		var donations, keys, pending, preview int64
		var hasApproved bool
		var identity sourceIdentity
		if err := rows.Scan(&identity.channel, &identity.connector, &identity.url, &donations, &keys, &pending, &preview, &hasApproved); err != nil {
			rows.Close()
			return empty, err
		}
		item.DonationCount = strconv.FormatInt(donations, 10)
		item.KeyCount = strconv.FormatInt(keys, 10)
		item.PendingDonationCount = strconv.FormatInt(pending, 10)
		result.Data = append(result.Data, item)
		previews = append(previews, preview)
		identities = append(identities, identity)
		usableCandidates = append(usableCandidates, hasApproved)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return empty, err
	}
	for i := range result.Data {
		item := &result.Data[i]
		item.SafeSource, err = sourcePreviewTx(ctx, tx, previews[i])
		if err != nil {
			return empty, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT `+sourceIdentitySQL+` FROM donation_keys dk WHERE dk.id=?`, previews[i]).Scan(&item.SourceKey); err != nil {
			return empty, err
		}
		if !usableCandidates[i] {
			item.UsableKeyCount = "0"
			continue
		}
		filtered, values := sourceSelectionSQL(filter, &identities[i], now)
		// Replace the selected key placeholder with the correlated safe ID. No
		// user input is interpolated; all filters stay bound parameters.
		usable := strings.Replace(sourceUsableSQL(), "WHERE dk.id=?", "WHERE dk.id=selected.id", 1)
		// Pending and unbound keys cannot serve a model. Avoid constructing
		// their reservation and catalog checks for every key in a large source.
		query := `SELECT COALESCE(SUM(CASE WHEN selected.donation_status='approved'
AND EXISTS(SELECT 1 FROM charity_model_bindings binding WHERE binding.donation_key_id=selected.id)
THEN (` + usable + `) ELSE 0 END),0) FROM (` + filtered + `) selected`
		var count int64
		if err := tx.QueryRowContext(ctx, query, append([]any{now, now}, values...)...).Scan(&count); err != nil {
			return empty, fmt.Errorf("donation: count usable source keys: %w", err)
		}
		item.UsableKeyCount = strconv.FormatInt(count, 10)
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) SourcesAdminPage(ctx context.Context, filter SourceFilter, page pagination.Request) (Page[DonationSource], error) {
	return s.sourcesPage(ctx, reviewerAdmin, 0, filter, page)
}
func (s *Service) SourcesStewardPage(ctx context.Context, userID int64, filter SourceFilter, page pagination.Request) (Page[DonationSource], error) {
	return s.sourcesPage(ctx, reviewerSteward, userID, filter, page)
}

func (s *Service) sourceKeysPage(ctx context.Context, role reviewerRole, userID int64, source string, filter SourceFilter, page pagination.Request) (Page[ManagedKeySummary], error) {
	var empty Page[ManagedKeySummary]
	if ctx == nil || !validSourceKey(source) || !page.Valid() || !validSourceFilter(filter, true) {
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
	identity, err := resolveSourceTx(ctx, tx, source, now)
	if err != nil {
		return empty, err
	}
	if identity == nil {
		metadata, _, err := page.Window(0)
		if err != nil {
			return empty, err
		}
		return Page[ManagedKeySummary]{Data: []ManagedKeySummary{}, Pagination: &metadata}, nil
	}
	selection, args := sourceSelectionSQL(filter, identity, now)
	query := `SELECT id FROM (` + selection + `)`
	ids, metadata, err := browseIDs(ctx, tx, query, args, page)
	if err != nil {
		return empty, err
	}
	result := Page[ManagedKeySummary]{Data: make([]ManagedKeySummary, 0, len(ids)), Pagination: &metadata}
	for _, keyID := range ids {
		var donationID int64
		if err := tx.QueryRowContext(ctx, `SELECT donation_id FROM donation_keys WHERE id=?`, keyID).Scan(&donationID); err != nil {
			return empty, err
		}
		header, err := logicalHeaderTx(ctx, tx, donationID, now)
		if err != nil {
			return empty, err
		}
		keys, err := readDonationKeySelectionTx(ctx, tx, donationID, header.Status, now, []int64{keyID})
		if err != nil {
			return empty, err
		}
		if len(keys) != 1 {
			return empty, ErrInvariant
		}
		rules, err := recurringSummaryTx(ctx, tx, keyID, now)
		if err != nil {
			return empty, err
		}
		result.Data = append(result.Data, managedKeySummary(keys[0], header, rules))
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Service) SourceKeysAdminPage(ctx context.Context, source string, filter SourceFilter, page pagination.Request) (Page[ManagedKeySummary], error) {
	return s.sourceKeysPage(ctx, reviewerAdmin, 0, source, filter, page)
}
func (s *Service) SourceKeysStewardPage(ctx context.Context, userID int64, source string, filter SourceFilter, page pagination.Request) (Page[ManagedKeySummary], error) {
	return s.sourceKeysPage(ctx, reviewerSteward, userID, source, filter, page)
}

func validSourceKey(value string) bool {
	if len(value) != 47 || !strings.HasPrefix(value, "dsg_") {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value[4:])
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value[4:]
}

// The opaque digest is resolved once over source identities. Subsequent page,
// COUNT and usable queries use the native indexable identity components.
func resolveSourceTx(ctx context.Context, tx *sql.Tx, key string, now int64) (*sourceIdentity, error) {
	selection, args := sourceSelectionSQL(SourceFilter{Scope: "all"}, nil, now)
	query := `SELECT ` + sourceGroupColumns + ` FROM (SELECT ` + sourceGroupColumns + ` FROM (` + selection + `) GROUP BY ` + sourceGroupColumns + `)
WHERE nbi_donation_source(NULLIF(source_channel,''),source_connector,source_url)=? LIMIT 1`
	var identity sourceIdentity
	err := tx.QueryRowContext(ctx, query, append(args, key)...).Scan(&identity.channel, &identity.connector, &identity.url)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &identity, err
}
