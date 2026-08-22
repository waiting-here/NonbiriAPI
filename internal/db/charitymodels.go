package db

// Charity model repository: charity model definitions with dual pricing
// (per-request / per-token, milli-credits), the limited-time discount fields,
// bindings to donated keys, and the last-100-outcome ring buffer (frozen §J.2
// / §J.2.5 / §J.5, implementation contract §2.7).
//
// Frozen semantics implemented here:
//
//   - full_name is always '[公益]' || provider || '/' || model and UNIQUE: a
//     second model resolving to the same routing key is a conflict; the CHECK
//     in the schema makes a hand-corrupted row unreadable rather than silently
//     routable;
//   - pricing_mode selects exactly ONE price table; the non-current table's
//     columns are kept at 0 so no field ever has two meanings;
//   - enabling a per-token model fails closed while charity_token_reserve_milli
//     is unset or non-positive — checked in the SAME transaction as the write;
//   - discount_percent 0..100 with an optional [start,end) interval; the
//     effective-discount evaluation lives in EffectiveDiscountPercent so the
//     future routing rail and tests share one implementation;
//   - binding writes re-verify the full candidate predicate (charity model
//     enabled; donation approved+enabled and not expired; donation key enabled
//     and claimed; endpoint + physical key still alive and enabled; upstream id
//     present in that key's fetched cache) inside the INSERT..SELECT itself;
//   - stats are a fixed 100-slot ring updated O(1) in one transaction; reads
//     never aggregate request_logs.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CharityPrefix is the fixed full_name prefix of every charity model. It is a
// product constant (frozen §K): personal models must never enter this
// namespace.
const CharityPrefix = "[公益]"

// Pricing modes (closed enum).
const (
	CharityPricingPerRequest = "per_request"
	CharityPricingPerToken   = "per_token"
)

// CharityModel is one charity model definition. All prices/rewards are
// non-negative int64 milli-credit values; token prices are per MILLION tokens.
type CharityModel struct {
	ID       int64
	Provider string
	Model    string
	FullName string
	Enabled  bool

	PricingMode           string
	RequestUserPrice      int64
	RequestDonorReward    int64
	UncachedUserPrice     int64
	CacheWriteUserPrice   int64
	CacheReadUserPrice    int64
	OutputUserPrice       int64
	UncachedDonorReward   int64
	CacheWriteDonorReward int64
	CacheReadDonorReward  int64
	OutputDonorReward     int64

	DiscountPercent int
	DiscountStartAt *int64
	DiscountEndAt   *int64
	DiscountEnabled bool

	CreatedByUserID *int64
	CreatedAt       int64
	UpdatedAt       int64
}

// CharityModelBinding binds a charity model to a donated (key, upstream) pair.
type CharityModelBinding struct {
	ID                 int64
	CharityModelID     int64
	DonationKeyID      int64
	UpstreamModelID    string
	Ord                int64
	CreatedAt          int64
	EndpointBaseURL    string // resolved display snapshot of the bound endpoint
	KeyDisplayHead     string
	KeyDisplayTail     string
	DonationKeyEnabled bool
}

const charityModelSelectSQL = `
SELECT id, provider, model, full_name, enabled, pricing_mode,
       request_user_price, request_donor_reward,
       uncached_user_price, cache_write_user_price, cache_read_user_price, output_user_price,
       uncached_donor_reward, cache_write_donor_reward, cache_read_donor_reward, output_donor_reward,
       discount_percent, discount_start_at, discount_end_at, discount_enabled,
       created_by_user_id, created_at, updated_at
FROM charity_models`

func scanCharityModelRow(row *sql.Row) (CharityModel, error) {
	var m CharityModel
	var enabledInt, discountEnabledInt int
	var discountStart, discountEnd, createdBy sql.NullInt64
	err := row.Scan(&m.ID, &m.Provider, &m.Model, &m.FullName, &enabledInt, &m.PricingMode,
		&m.RequestUserPrice, &m.RequestDonorReward,
		&m.UncachedUserPrice, &m.CacheWriteUserPrice, &m.CacheReadUserPrice, &m.OutputUserPrice,
		&m.UncachedDonorReward, &m.CacheWriteDonorReward, &m.CacheReadDonorReward, &m.OutputDonorReward,
		&m.DiscountPercent, &discountStart, &discountEnd, &discountEnabledInt,
		&createdBy, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return CharityModel{}, err
	}
	m.Enabled = enabledInt == 1
	m.DiscountEnabled = discountEnabledInt == 1
	m.DiscountStartAt = nullInt64Ptr(discountStart)
	m.DiscountEndAt = nullInt64Ptr(discountEnd)
	m.CreatedByUserID = nullInt64Ptr(createdBy)
	return m, nil
}

func scanCharityModelRows(rows *sql.Rows) ([]CharityModel, error) {
	out := make([]CharityModel, 0, 16)
	for rows.Next() {
		var (
			m                                     CharityModel
			enabledInt, discountEnabledInt        int
			discountStart, discountEnd, createdBy sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.Provider, &m.Model, &m.FullName, &enabledInt, &m.PricingMode,
			&m.RequestUserPrice, &m.RequestDonorReward,
			&m.UncachedUserPrice, &m.CacheWriteUserPrice, &m.CacheReadUserPrice, &m.OutputUserPrice,
			&m.UncachedDonorReward, &m.CacheWriteDonorReward, &m.CacheReadDonorReward, &m.OutputDonorReward,
			&m.DiscountPercent, &discountStart, &discountEnd, &discountEnabledInt,
			&createdBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan charity model: %w", err)
		}
		m.Enabled = enabledInt == 1
		m.DiscountEnabled = discountEnabledInt == 1
		m.DiscountStartAt = nullInt64Ptr(discountStart)
		m.DiscountEndAt = nullInt64Ptr(discountEnd)
		m.CreatedByUserID = nullInt64Ptr(createdBy)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate charity models: %w", err)
	}
	return out, nil
}

func validPricingMode(m string) bool {
	return m == CharityPricingPerRequest || m == CharityPricingPerToken
}

// zeroNonCurrentPrices keeps exactly one interpretable price table: the fields
// of the non-selected mode are forced to 0 before persistence (frozen §2.7).
func zeroNonCurrentPrices(m *CharityModel) {
	if m.PricingMode == CharityPricingPerRequest {
		m.UncachedUserPrice, m.CacheWriteUserPrice, m.CacheReadUserPrice, m.OutputUserPrice = 0, 0, 0, 0
		m.UncachedDonorReward, m.CacheWriteDonorReward, m.CacheReadDonorReward, m.OutputDonorReward = 0, 0, 0, 0
	} else {
		m.RequestUserPrice, m.RequestDonorReward = 0, 0
	}
}

// validateCharityModel performs the structural checks shared by create and
// update: mode membership, non-negative prices, discount bounds and interval
// sanity. Text bounds for provider/model are enforced by the service layer
// (they share the platform's bounded-text policy).
func validateCharityModel(m *CharityModel) error {
	if !validPricingMode(m.PricingMode) {
		return errors.New("invalid pricing_mode")
	}
	prices := []int64{
		m.RequestUserPrice, m.RequestDonorReward,
		m.UncachedUserPrice, m.CacheWriteUserPrice, m.CacheReadUserPrice, m.OutputUserPrice,
		m.UncachedDonorReward, m.CacheWriteDonorReward, m.CacheReadDonorReward, m.OutputDonorReward,
	}
	for _, p := range prices {
		if p < 0 {
			return errors.New("negative price")
		}
	}
	zeroNonCurrentPrices(m)
	if m.DiscountPercent < 0 || m.DiscountPercent > 100 {
		return errors.New("discount_percent out of range")
	}
	if m.DiscountStartAt != nil && m.DiscountEndAt != nil && *m.DiscountStartAt >= *m.DiscountEndAt {
		return errors.New("discount interval must satisfy start < end")
	}
	return nil
}

// CreateCharityModel inserts one charity model. The derived full_name is
// UNIQUE; a duplicate routing key is ErrConflict. Enabling a per-token model
// requires a configured positive charity_token_reserve_milli IN THE SAME
// TRANSACTION (fail closed). now is caller-supplied; actorUserID may be 0
// (recorded as NULL).
func (s *Store) CreateCharityModel(ctx context.Context, m CharityModel, actorUserID, now int64) (CharityModel, error) {
	if now <= 0 {
		return CharityModel{}, errors.New("timestamp is required")
	}
	if err := validateCharityModel(&m); err != nil {
		return CharityModel{}, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}
	fullName := CharityPrefix + m.Provider + "/" + m.Model

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityModel{}, fmt.Errorf("create charity model: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if m.Enabled && m.PricingMode == CharityPricingPerToken {
		if err := requireTokenReserveTx(ctx, tx); err != nil {
			return CharityModel{}, err
		}
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO charity_models
	(provider, model, full_name, enabled, pricing_mode,
	 request_user_price, request_donor_reward,
	 uncached_user_price, cache_write_user_price, cache_read_user_price, output_user_price,
	 uncached_donor_reward, cache_write_donor_reward, cache_read_donor_reward, output_donor_reward,
	 discount_percent, discount_start_at, discount_end_at, discount_enabled,
	 created_by_user_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Provider, m.Model, fullName, boolInt(m.Enabled), m.PricingMode,
		m.RequestUserPrice, m.RequestDonorReward,
		m.UncachedUserPrice, m.CacheWriteUserPrice, m.CacheReadUserPrice, m.OutputUserPrice,
		m.UncachedDonorReward, m.CacheWriteDonorReward, m.CacheReadDonorReward, m.OutputDonorReward,
		m.DiscountPercent, m.DiscountStartAt, m.DiscountEndAt, boolInt(m.DiscountEnabled),
		nullableUserID(actorUserID), now, now)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM charity_models WHERE full_name=?`, fullName); derr != nil {
				return CharityModel{}, derr
			}
		}
		return CharityModel{}, fmt.Errorf("create charity model: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return CharityModel{}, fmt.Errorf("create charity model: id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CharityModel{}, fmt.Errorf("create charity model: commit: %w", err)
	}
	committed = true
	return s.GetCharityModel(ctx, id)
}

// UpdateCharityModel atomically updates the mutable fields of one charity
// model. A nil field leaves it unchanged. The enable transition re-checks the
// token-reserve precondition in the same transaction as the write.
func (s *Store) UpdateCharityModel(ctx context.Context, id int64, upd CharityModelUpdate, now int64) (CharityModel, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityModel{}, fmt.Errorf("update charity model: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRowContext(ctx, charityModelSelectSQL+` WHERE id=?`, id)
	current, err := scanCharityModelRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityModel{}, ErrNotFound
	}
	if err != nil {
		return CharityModel{}, fmt.Errorf("update charity model: read: %w", err)
	}

	next := current
	if upd.Provider != nil {
		next.Provider = *upd.Provider
	}
	if upd.Model != nil {
		next.Model = *upd.Model
	}
	if upd.Enabled != nil {
		next.Enabled = *upd.Enabled
	}
	if upd.PricingMode != nil {
		if !validPricingMode(*upd.PricingMode) {
			return CharityModel{}, fmt.Errorf("%w: invalid pricing_mode", ErrInvalidValue)
		}
		next.PricingMode = *upd.PricingMode
	}
	if upd.Prices != nil {
		p := *upd.Prices
		if p.RequestUserPrice != nil {
			next.RequestUserPrice = *p.RequestUserPrice
		}
		if p.RequestDonorReward != nil {
			next.RequestDonorReward = *p.RequestDonorReward
		}
		if p.UncachedUserPrice != nil {
			next.UncachedUserPrice = *p.UncachedUserPrice
		}
		if p.CacheWriteUserPrice != nil {
			next.CacheWriteUserPrice = *p.CacheWriteUserPrice
		}
		if p.CacheReadUserPrice != nil {
			next.CacheReadUserPrice = *p.CacheReadUserPrice
		}
		if p.OutputUserPrice != nil {
			next.OutputUserPrice = *p.OutputUserPrice
		}
		if p.UncachedDonorReward != nil {
			next.UncachedDonorReward = *p.UncachedDonorReward
		}
		if p.CacheWriteDonorReward != nil {
			next.CacheWriteDonorReward = *p.CacheWriteDonorReward
		}
		if p.CacheReadDonorReward != nil {
			next.CacheReadDonorReward = *p.CacheReadDonorReward
		}
		if p.OutputDonorReward != nil {
			next.OutputDonorReward = *p.OutputDonorReward
		}
	}
	if err := validateCharityModel(&next); err != nil {
		return CharityModel{}, fmt.Errorf("%w: %v", ErrInvalidValue, err)
	}
	if next.Enabled != current.Enabled && next.Enabled &&
		next.PricingMode == CharityPricingPerToken {
		if err := requireTokenReserveTx(ctx, tx); err != nil {
			return CharityModel{}, err
		}
	}

	sets := make([]string, 0, 20)
	args := make([]any, 0, 24)
	set := func(col string, v any) {
		sets = append(sets, col+"=?")
		args = append(args, v)
	}
	if next.Provider != current.Provider || next.Model != current.Model {
		set("provider", next.Provider)
		set("model", next.Model)
		set("full_name", CharityPrefix+next.Provider+"/"+next.Model)
	}
	if next.Enabled != current.Enabled {
		set("enabled", boolInt(next.Enabled))
	}
	if next.PricingMode != current.PricingMode {
		set("pricing_mode", next.PricingMode)
	}
	pricePairs := []struct {
		col      string
		cur, nxt int64
	}{
		{"request_user_price", current.RequestUserPrice, next.RequestUserPrice},
		{"request_donor_reward", current.RequestDonorReward, next.RequestDonorReward},
		{"uncached_user_price", current.UncachedUserPrice, next.UncachedUserPrice},
		{"cache_write_user_price", current.CacheWriteUserPrice, next.CacheWriteUserPrice},
		{"cache_read_user_price", current.CacheReadUserPrice, next.CacheReadUserPrice},
		{"output_user_price", current.OutputUserPrice, next.OutputUserPrice},
		{"uncached_donor_reward", current.UncachedDonorReward, next.UncachedDonorReward},
		{"cache_write_donor_reward", current.CacheWriteDonorReward, next.CacheWriteDonorReward},
		{"cache_read_donor_reward", current.CacheReadDonorReward, next.CacheReadDonorReward},
		{"output_donor_reward", current.OutputDonorReward, next.OutputDonorReward},
	}
	for _, p := range pricePairs {
		if p.cur != p.nxt {
			set(p.col, p.nxt)
		}
	}
	if upd.DiscountPercent != nil {
		if *upd.DiscountPercent < 0 || *upd.DiscountPercent > 100 {
			return CharityModel{}, fmt.Errorf("%w: discount_percent out of range", ErrInvalidValue)
		}
		if *upd.DiscountPercent != next.DiscountPercent {
			next.DiscountPercent = *upd.DiscountPercent
			set("discount_percent", *upd.DiscountPercent)
		}
	}
	if upd.DiscountEnabled != nil && *upd.DiscountEnabled != next.DiscountEnabled {
		next.DiscountEnabled = *upd.DiscountEnabled
		set("discount_enabled", boolInt(next.DiscountEnabled))
	}
	if upd.ClearDiscountStart {
		next.DiscountStartAt = nil
		set("discount_start_at", nil)
	} else if upd.DiscountStartAt != nil {
		next.DiscountStartAt = upd.DiscountStartAt
		set("discount_start_at", upd.DiscountStartAt)
	}
	if upd.ClearDiscountEnd {
		next.DiscountEndAt = nil
		set("discount_end_at", nil)
	} else if upd.DiscountEndAt != nil {
		next.DiscountEndAt = upd.DiscountEndAt
		set("discount_end_at", upd.DiscountEndAt)
	}
	// Interval sanity after applying all discount fields.
	if next.DiscountStartAt != nil && next.DiscountEndAt != nil && *next.DiscountStartAt >= *next.DiscountEndAt {
		return CharityModel{}, fmt.Errorf("%w: discount interval must satisfy start < end", ErrInvalidValue)
	}

	if len(sets) == 0 {
		if err := tx.Commit(); err != nil {
			return CharityModel{}, fmt.Errorf("update charity model: commit noop: %w", err)
		}
		committed = true
		return current, nil
	}
	set("updated_at", now)
	args = append(args, id)
	query := `UPDATE charity_models SET `
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += ` WHERE id=?`
	// #nosec G202 -- sets contains only constant column fragments selected above
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM charity_models WHERE full_name=? AND id<>?`,
				CharityPrefix+next.Provider+"/"+next.Model, id); derr != nil {
				return CharityModel{}, derr
			}
		}
		return CharityModel{}, fmt.Errorf("update charity model: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return CharityModel{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return CharityModel{}, fmt.Errorf("update charity model: commit: %w", err)
	}
	committed = true
	return s.GetCharityModel(ctx, id)
}

// CharityModelUpdate carries the PATCH fields of one charity model. Pointer =
// present. Clear* flags express explicit null clears of the nullable interval
// bounds (JSON null cannot be smuggled through a *int64).
type CharityModelUpdate struct {
	Provider    *string
	Model       *string
	Enabled     *bool
	PricingMode *string
	Prices      *CharityModelPrices

	DiscountPercent    *int
	DiscountEnabled    *bool
	DiscountStartAt    *int64
	ClearDiscountStart bool
	DiscountEndAt      *int64
	ClearDiscountEnd   bool
}

// CharityModelPrices groups the ten price/reward fields for a partial update.
type CharityModelPrices struct {
	RequestUserPrice      *int64
	RequestDonorReward    *int64
	UncachedUserPrice     *int64
	CacheWriteUserPrice   *int64
	CacheReadUserPrice    *int64
	OutputUserPrice       *int64
	UncachedDonorReward   *int64
	CacheWriteDonorReward *int64
	CacheReadDonorReward  *int64
	OutputDonorReward     *int64
}

// GetCharityModel returns one charity model by id (no ownership scope: these
// are site-wide resources managed behind authenticated admin/steward frames).
func (s *Store) GetCharityModel(ctx context.Context, id int64) (CharityModel, error) {
	row := s.db.QueryRowContext(ctx, charityModelSelectSQL+` WHERE id=?`, id)
	m, err := scanCharityModelRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityModel{}, ErrNotFound
	}
	if err != nil {
		return CharityModel{}, fmt.Errorf("get charity model: %w", err)
	}
	return m, nil
}

// ListCharityModels returns charity models newest-first, optionally filtered
// by enabled state (enabledOnly), bounded by limit/offset.
func (s *Store) ListCharityModels(ctx context.Context, enabledOnly bool, limit, offset int) ([]CharityModel, int, error) {
	if limit <= 0 {
		return nil, 0, ErrNotFound
	}
	where := ""
	args := []any{}
	if enabledOnly {
		where = `WHERE enabled=1`
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM charity_models `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count charity models: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, charityModelSelectSQL+` `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list charity models: %w", err)
	}
	defer rows.Close()
	out, err := scanCharityModelRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// DeleteCharityModel removes one charity model; its bindings and stats cascade
// (the outcomes/stats tables exist only to describe the model). This is a
// management-plane delete of a configuration object, not an audit-record purge.
func (s *Store) DeleteCharityModel(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM charity_models WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete charity model: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete charity model: rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// nullableUserID maps a zero/unknown actor id to SQL NULL so an unattributed
// management action never violates the users foreign key.
func nullableUserID(id int64) any {
	if id > 0 {
		return id
	}
	return nil
}

// requireTokenReserveTx fails closed when charity_token_reserve_milli is not
// configured as a positive amount. It runs inside the same transaction as the
// enable/create write so no interleaving can flip a token-priced model live
// without a reserve price.
func requireTokenReserveTx(ctx context.Context, tx *sql.Tx) error {
	raw, err := readSiteConfigRowTx(ctx, tx, "charity_token_reserve_milli")
	if err != nil {
		return err
	}
	v, perr := parseCheckinAmount(raw, -1) // missing row -> sentinel default (-1 = unset)
	if perr != nil || v <= 0 {
		return ErrCharityTokenReserveMissing
	}
	return nil
}

// EffectiveDiscountPercent evaluates the [start,end) interval semantics of one
// model's discount configuration at instant now: disabled, not-yet-open or
// already-closed intervals yield 100 (original price). Exported so the future
// routing/pricing rail and tests share one implementation.
func (m CharityModel) EffectiveDiscountPercent(now int64) int {
	if !m.DiscountEnabled {
		return 100
	}
	if m.DiscountStartAt != nil && now < *m.DiscountStartAt {
		return 100
	}
	if m.DiscountEndAt != nil && now >= *m.DiscountEndAt {
		return 100
	}
	return m.DiscountPercent
}

// --- bindings ---------------------------------------------------------------

const charityBindingSelectSQL = `
SELECT b.id, b.charity_model_id, b.donation_key_id, b.upstream_model_id, b.ord, b.created_at,
       e.base_url, dk.display_head, dk.display_tail, dk.enabled
FROM charity_model_bindings b
JOIN charity_models cm ON b.charity_model_id = cm.id
JOIN donation_keys dk ON dk.id = b.donation_key_id
LEFT JOIN endpoint_keys ek ON ek.id = dk.endpoint_key_id
LEFT JOIN endpoints e ON ek.endpoint_id = e.id
WHERE b.charity_model_id=?`

func scanCharityBindingRow(row *sql.Row) (CharityModelBinding, error) {
	var b CharityModelBinding
	var keyEnabled int
	err := row.Scan(&b.ID, &b.CharityModelID, &b.DonationKeyID, &b.UpstreamModelID, &b.Ord, &b.CreatedAt,
		&b.EndpointBaseURL, &b.KeyDisplayHead, &b.KeyDisplayTail, &keyEnabled)
	if err != nil {
		return CharityModelBinding{}, err
	}
	b.DonationKeyEnabled = keyEnabled == 1
	return b, nil
}

// ListCharityBindings returns the bindings of one charity model ordered by
// (ord, id), resolved with display-only fragments of the bound resources.
func (s *Store) ListCharityBindings(ctx context.Context, modelID int64) ([]CharityModelBinding, error) {
	rows, err := s.db.QueryContext(ctx, charityBindingSelectSQL+` ORDER BY b.ord, b.id`, modelID)
	if err != nil {
		return nil, fmt.Errorf("list charity bindings: %w", err)
	}
	defer rows.Close()
	out := make([]CharityModelBinding, 0, 8)
	for rows.Next() {
		var b CharityModelBinding
		var keyEnabled int
		if err := rows.Scan(&b.ID, &b.CharityModelID, &b.DonationKeyID, &b.UpstreamModelID, &b.Ord, &b.CreatedAt,
			&b.EndpointBaseURL, &b.KeyDisplayHead, &b.KeyDisplayTail, &keyEnabled); err != nil {
			return nil, fmt.Errorf("scan charity binding: %w", err)
		}
		b.DonationKeyEnabled = keyEnabled == 1
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate charity bindings: %w", err)
	}
	return out, nil
}

// CreateCharityBinding validates the ENTIRE candidate predicate inside one
// INSERT..SELECT (never read-then-write): the charity model must be enabled,
// the donation approved+enabled and unexpired, the donation key enabled with a
// live claim, the physical endpoint/key alive and enabled, and the upstream id
// present in that key's fetched cache. Zero rows map to ErrNotFound (the exact
// failing condition is not disclosed); a duplicate triple is ErrConflict.
func (s *Store) CreateCharityBinding(ctx context.Context, modelID, donationKeyID int64, upstreamModelID string, ord, now int64) (CharityModelBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityModelBinding{}, fmt.Errorf("create charity binding: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	res, err := tx.ExecContext(ctx, `
INSERT INTO charity_model_bindings (charity_model_id, donation_key_id, upstream_model_id, ord, created_at)
SELECT cm.id, dk.id, ?, ?, ?
FROM charity_models cm
JOIN donations d ON d.status='approved' AND d.enabled=1
  AND (d.expires_at IS NULL OR d.expires_at > ?)
JOIN donation_keys dk ON dk.id = ? AND dk.donation_id = d.id AND dk.enabled=1
JOIN donation_key_claims c ON c.donation_key_id = dk.id
JOIN endpoint_keys ek ON ek.id = dk.endpoint_key_id AND ek.enabled=1
JOIN endpoints e ON ek.endpoint_id = e.id AND e.enabled=1
JOIN fetched_models fm ON fm.endpoint_key_id = ek.id AND fm.upstream_model_id = ?
WHERE cm.id = ? AND cm.enabled=1`,
		upstreamModelID, ord, now, now, donationKeyID, upstreamModelID, modelID)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM charity_model_bindings WHERE charity_model_id=? AND donation_key_id=? AND upstream_model_id=?`,
				modelID, donationKeyID, upstreamModelID); derr != nil {
				return CharityModelBinding{}, derr
			}
		}
		return CharityModelBinding{}, fmt.Errorf("create charity binding: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return CharityModelBinding{}, fmt.Errorf("create charity binding: rows: %w", err)
	}
	if affected == 0 {
		return CharityModelBinding{}, ErrNotFound
	}
	id, err := res.LastInsertId()
	if err != nil {
		return CharityModelBinding{}, fmt.Errorf("create charity binding: id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CharityModelBinding{}, fmt.Errorf("create charity binding: commit: %w", err)
	}
	committed = true
	return CharityModelBinding{
		ID: id, CharityModelID: modelID, DonationKeyID: donationKeyID,
		UpstreamModelID: upstreamModelID, Ord: ord, CreatedAt: now,
	}, nil
}

// UpdateCharityBinding updates ord and/or upstream_model_id of one binding.
// The candidate validity of the RESULTING tuple is re-verified in SQL; the
// bound donation key never changes through this path.
func (s *Store) UpdateCharityBinding(ctx context.Context, modelID, bindingID int64, ord *int64, upstreamModelID *string, now int64) (CharityModelBinding, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityModelBinding{}, fmt.Errorf("update charity binding: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRowContext(ctx, charityBindingSelectSQL+` AND b.id=?`, modelID, bindingID)
	current, err := scanCharityBindingRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityModelBinding{}, ErrNotFound
	}
	if err != nil {
		return CharityModelBinding{}, fmt.Errorf("update charity binding: read: %w", err)
	}
	newOrd := current.Ord
	if ord != nil {
		newOrd = *ord
	}
	newUpstream := current.UpstreamModelID
	if upstreamModelID != nil {
		newUpstream = *upstreamModelID
	}
	if newOrd == current.Ord && newUpstream == current.UpstreamModelID {
		if err := tx.Commit(); err != nil {
			return CharityModelBinding{}, fmt.Errorf("update charity binding: commit noop: %w", err)
		}
		committed = true
		return current, nil
	}

	sets := make([]string, 0, 2)
	args := make([]any, 0, 6)
	if newOrd != current.Ord {
		sets = append(sets, "ord=?")
		args = append(args, newOrd)
	}
	if newUpstream != current.UpstreamModelID {
		sets = append(sets, "upstream_model_id=?")
		args = append(args, newUpstream)
	}
	// Candidate re-validation of the RESULTING tuple: same predicate family as
	// the create path, applied inside this UPDATE's EXISTS clause.
	args = append(args, bindingID, modelID, now, newUpstream, modelID)
	query := `UPDATE charity_model_bindings SET ` + joinSets(sets) + `
WHERE id=? AND charity_model_id=?
  AND EXISTS (
    SELECT 1 FROM charity_models cm
    JOIN donations d ON d.status='approved' AND d.enabled=1 AND (d.expires_at IS NULL OR d.expires_at > ?)
    JOIN donation_keys dk ON dk.donation_id = d.id AND dk.enabled=1
    JOIN donation_key_claims c ON c.donation_key_id = dk.id
    JOIN endpoint_keys ek ON ek.id = dk.endpoint_key_id AND ek.enabled=1
    JOIN endpoints e ON ek.endpoint_id = e.id AND e.enabled=1
    JOIN fetched_models fm ON fm.endpoint_key_id = ek.id AND fm.upstream_model_id = ?
    WHERE cm.id = ? AND cm.enabled=1 AND charity_model_bindings.donation_key_id = dk.id
  )`
	// #nosec G202 -- sets contains only constant column fragments selected above
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		if isConstraintError(err) {
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM charity_model_bindings WHERE charity_model_id=? AND donation_key_id=? AND upstream_model_id=? AND id<>?`,
				modelID, current.DonationKeyID, newUpstream, bindingID); derr != nil {
				return CharityModelBinding{}, derr
			}
		}
		return CharityModelBinding{}, fmt.Errorf("update charity binding: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return CharityModelBinding{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return CharityModelBinding{}, fmt.Errorf("update charity binding: commit: %w", err)
	}
	committed = true
	updated, err := scanCharityBindingRow(
		s.db.QueryRowContext(ctx, charityBindingSelectSQL+` AND b.id=?`, modelID, bindingID))
	if err != nil {
		return CharityModelBinding{}, fmt.Errorf("update charity binding: reread: %w", err)
	}
	return updated, nil
}

// DeleteCharityBinding removes one binding of one charity model.
func (s *Store) DeleteCharityBinding(ctx context.Context, modelID, bindingID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM charity_model_bindings WHERE id=? AND charity_model_id=?`, bindingID, modelID)
	if err != nil {
		return fmt.Errorf("delete charity binding: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete charity binding: rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// --- last-100-outcome ring buffer -------------------------------------------

// RecordCharityOutcome advances the model's ring buffer by one outcome inside
// one transaction: the outgoing slot value (if any) is subtracted, the new
// outcome written, and next_slot/sample_count/success_count rolled forward.
// O(1); safe against concurrent writers through the single-writer handle.
func (s *Store) RecordCharityOutcome(ctx context.Context, modelID int64, success bool, now int64) error {
	successInt := 0
	if success {
		successInt = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record charity outcome: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// The model must still exist (FK on stats/outcomes cascades otherwise).
	// Checked on the SAME transaction: with a single-connection pool an
	// out-of-band read here would deadlock against this very transaction.
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM charity_models WHERE id=?`, modelID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("record charity outcome: read model: %w", err)
	}
	var nextSlot, sampleCount, successCount int
	err = tx.QueryRowContext(ctx,
		`SELECT next_slot, sample_count, success_count FROM charity_model_stats WHERE model_id=?`, modelID).
		Scan(&nextSlot, &sampleCount, &successCount)
	if errors.Is(err, sql.ErrNoRows) {
		nextSlot, sampleCount, successCount = 0, 0, 0
	} else if err != nil {
		return fmt.Errorf("record charity outcome: read stats: %w", err)
	}
	var oldSuccess int
	err = tx.QueryRowContext(ctx,
		`SELECT success FROM charity_model_outcomes WHERE model_id=? AND slot=?`, modelID, nextSlot).
		Scan(&oldSuccess)
	if errors.Is(err, sql.ErrNoRows) {
		oldSuccess = -1 // empty slot
	} else if err != nil {
		return fmt.Errorf("record charity outcome: read slot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO charity_model_outcomes (model_id, slot, success, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT(model_id, slot) DO UPDATE SET success=excluded.success, created_at=excluded.created_at`,
		modelID, nextSlot, successInt, now); err != nil {
		return fmt.Errorf("record charity outcome: write slot: %w", err)
	}
	sampleCount = min(sampleCount+1, 100)
	successCount += successInt
	if oldSuccess == 1 {
		successCount--
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO charity_model_stats (model_id, next_slot, sample_count, success_count) VALUES (?, ?, ?, ?)
ON CONFLICT(model_id) DO UPDATE SET next_slot=excluded.next_slot, sample_count=excluded.sample_count, success_count=excluded.success_count`,
		modelID, (nextSlot+1)%100, sampleCount, successCount); err != nil {
		return fmt.Errorf("record charity outcome: write stats: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record charity outcome: commit: %w", err)
	}
	committed = true
	return nil
}

// CharitySuccessRate is the O(1) projection of the ring buffer.
type CharitySuccessRate struct {
	SampleCount  int
	SuccessCount int
}

// GetCharitySuccessRate returns the rolling last-100 counters for one model.
// A model with no recorded outcome yields zeros.
func (s *Store) GetCharitySuccessRate(ctx context.Context, modelID int64) (CharitySuccessRate, error) {
	var rate CharitySuccessRate
	err := s.db.QueryRowContext(ctx,
		`SELECT sample_count, success_count FROM charity_model_stats WHERE model_id=?`, modelID).
		Scan(&rate.SampleCount, &rate.SuccessCount)
	if errors.Is(err, sql.ErrNoRows) {
		return CharitySuccessRate{}, nil
	}
	if err != nil {
		return CharitySuccessRate{}, fmt.Errorf("get charity success rate: %w", err)
	}
	return rate, nil
}

// boolInt is provided by sessions.go within this package.
