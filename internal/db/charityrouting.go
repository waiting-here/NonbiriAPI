package db

// Charity routing projections: the deterministic [公益] namespace resolution,
// the full candidate predicate re-verified in SQL on every read, the /v1/models
// inclusion projection, and the dispatch-time target revalidation.
//
// Frozen semantics (product §J/§K, implementation contract §2.7/§6.2):
//
//   - a charity model is addressable ONLY by its '[公益]'-prefixed full_name;
//     personal models can never enter this namespace (creation-time provider
//     validation) and charity candidates never consult personal resources:
//     the two namespaces are disjoint by construction, not by filtering;
//   - every candidate row is admitted only while the whole chain is alive in
//     the SAME SQL statement: charity model enabled, donation approved +
//     enabled + unexpired, donation key enabled, physical claim held, endpoint
//     + endpoint key alive and enabled, fetched-model cache row present and ok;
//   - no read-then-filter: a resource that died between selection and dispatch
//     fails closed at the dispatch-time target read instead.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

// CharityCandidate is one routable donated (binding, key) pair. The limit
// fields mirror donation_keys so the per-key admission layer can pre-check
// without a second read; the authoritative cap check stays in the reservation
// transaction.
type CharityCandidate struct {
	BindingID       int64
	DonationKeyID   int64
	EndpointID      int64
	EndpointKeyID   int64
	ConnectorType   string
	UpstreamModelID string
	Ord             int64
	DonorUserID     int64
	BaseURL         string

	MaxConcurrency  int64
	RPMLimit        int64
	CreditsUsageCap int64 // milli-credits; 0 = unlimited
	CreditsUsed     int64
	CreditsReserved int64
}

// CharityRoute is one resolvable charity model with its complete usable
// candidate set in (ord,id) order.
type CharityRoute struct {
	Model      CharityModel
	Candidates []CharityCandidate
}

const charityCandidatePredicate = `
FROM charity_model_bindings b
JOIN charity_models cm
  ON cm.id = b.charity_model_id
 AND cm.enabled = 1
 AND cm.full_name = ?
JOIN donations d
  ON d.status = 'approved'
 AND d.enabled = 1
 AND (d.expires_at IS NULL OR d.expires_at > ?)
JOIN donation_keys dk
  ON dk.id = b.donation_key_id
 AND dk.donation_id = d.id
 AND dk.enabled = 1
JOIN donation_key_claims c
  ON c.donation_key_id = dk.id
JOIN endpoint_keys ek
  ON ek.id = dk.endpoint_key_id
 AND ek.enabled = 1
JOIN endpoints e
  ON e.id = ek.endpoint_id
 AND e.enabled = 1
JOIN fetched_models fm
  ON fm.endpoint_key_id = ek.id
 AND fm.upstream_model_id = b.upstream_model_id
 AND fm.status = 'ok'
`

const charityCandidateSelectSQL = `
SELECT b.id, dk.id, e.id, ek.id, e.connector_type, b.upstream_model_id, b.ord, d.user_id, e.base_url,
       dk.max_concurrency, dk.rpm_limit, dk.credits_usage_cap, dk.credits_used, dk.credits_reserved` +
	charityCandidatePredicate

func scanCharityCandidates(rows *sql.Rows, limit int) ([]CharityCandidate, error) {
	defer rows.Close()
	candidates := make([]CharityCandidate, 0, min(limit, 8))
	for rows.Next() {
		if len(candidates) == limit {
			return nil, ErrForwardProjectionLimit
		}
		var c CharityCandidate
		if err := rows.Scan(&c.BindingID, &c.DonationKeyID, &c.EndpointID, &c.EndpointKeyID, &c.ConnectorType,
			&c.UpstreamModelID, &c.Ord, &c.DonorUserID, &c.BaseURL,
			&c.MaxConcurrency, &c.RPMLimit, &c.CreditsUsageCap, &c.CreditsUsed, &c.CreditsReserved); err != nil {
			return nil, fmt.Errorf("scan charity candidate: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate charity candidates: %w", err)
	}
	return candidates, nil
}

// resolveCharityRouteTx resolves one charity model and its usable candidates
// inside the caller's transaction. now gates the donation expiry predicate.
func resolveCharityRouteTx(ctx context.Context, tx *sql.Tx, fullName string, now int64, limit int) (CharityRoute, error) {
	var route CharityRoute
	row := tx.QueryRowContext(ctx, charityModelSelectSQL+` WHERE full_name=?`, fullName)
	model, err := scanCharityModelRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return CharityRoute{}, ErrNotFound
	}
	if err != nil {
		return CharityRoute{}, fmt.Errorf("resolve charity model: %w", err)
	}
	if !model.Enabled {
		// A disabled model is indistinguishable from an absent one at the
		// routing boundary: both are ErrNotFound so availability state is not
		// disclosed through the API exit.
		return CharityRoute{}, ErrNotFound
	}
	route.Model = model
	if model.FlattenToolCalls {
		// Flatten is an OpenAI-only model policy. If the persisted state is
		// damaged despite the write-path invariant, fail closed instead of
		// filtering the incompatible binding and routing the remainder.
		var incompatible int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM charity_model_bindings b
JOIN donation_keys dk ON dk.id=b.donation_key_id
JOIN endpoint_keys ek ON ek.id=dk.endpoint_key_id
JOIN endpoints e ON e.id=ek.endpoint_id
WHERE b.charity_model_id=? AND e.connector_type <> 'openai-compatible'`, model.ID).Scan(&incompatible); err != nil {
			return CharityRoute{}, fmt.Errorf("validate charity flatten policy: %w", err)
		}
		if incompatible != 0 {
			return CharityRoute{}, ErrConflict
		}
	}
	rows, err := tx.QueryContext(ctx, charityCandidateSelectSQL+` ORDER BY b.ord, b.id LIMIT ?`,
		fullName, now, limit+1)
	if err != nil {
		return CharityRoute{}, fmt.Errorf("query charity candidates: %w", err)
	}
	candidates, err := scanCharityCandidates(rows, limit)
	if err != nil {
		return CharityRoute{}, fmt.Errorf("resolve charity candidates: %w", err)
	}
	route.Candidates = candidates
	return route, nil
}

// ResolveCharityRoute resolves one [公益] model with its complete usable
// candidate set after running the lazy expiry sweep. An absent or disabled
// model is ErrNotFound.
func (s *Store) ResolveCharityRoute(ctx context.Context, fullName string, now int64, limit int) (CharityRoute, error) {
	if fullName == "" || limit <= 0 {
		return CharityRoute{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CharityRoute{}, fmt.Errorf("begin charity route: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := sweepExpiredDonationsTx(ctx, tx, now); err != nil {
		return CharityRoute{}, err
	}
	route, err := resolveCharityRouteTx(ctx, tx, fullName, now, limit)
	if err != nil {
		return CharityRoute{}, err
	}
	if err := tx.Commit(); err != nil {
		return CharityRoute{}, fmt.Errorf("commit charity route: %w", err)
	}
	return route, nil
}

// ListCallerCharityModels returns the /v1/models projection entries of every
// ENABLED charity model with at least one valid candidate, gated by the
// authoritative site-wide charity switch read inside the same transaction
// (frozen §6.2: only while charity_enabled). The switch-off result is an empty
// list, never an error, so the shared /v1/models response shape is stable.
func (s *Store) ListCallerCharityModels(ctx context.Context, now int64, limit int) ([]CallerModel, error) {
	if limit <= 0 {
		return nil, ErrForwardProjectionLimit
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list caller charity models: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	enabled, err := readCharityEnabledTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return []CallerModel{}, nil
	}
	rows, err := tx.QueryContext(ctx, `
SELECT cm.full_name, cm.provider, cm.created_at
FROM charity_models cm
WHERE cm.enabled = 1
  AND EXISTS (
    SELECT 1 `+charityCandidatePredicate+`
      AND b.charity_model_id = cm.id
  )
ORDER BY cm.id
LIMIT ?`, now, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list caller charity models: %w", err)
	}
	defer rows.Close()
	models := make([]CallerModel, 0, min(limit, 32))
	for rows.Next() {
		if len(models) == limit {
			return nil, ErrForwardProjectionLimit
		}
		var model CallerModel
		if err := rows.Scan(&model.FullName, &model.Provider, &model.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan caller charity model: %w", err)
		}
		models = append(models, model)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate caller charity models: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit caller charity models: %w", err)
	}
	return models, nil
}

// CharityForwardTarget is the charity dispatch projection: the same sealed
// shape as the personal ForwardTarget plus the donor identity needed to build
// the authenticated credential context (the envelope is bound to the DONOR's
// ownership scope, never to the consumer's).
type CharityForwardTarget struct {
	ForwardTarget
	BindingID     int64
	DonationKeyID int64
	DonorUserID   int64
}

// GetCharityForwardTarget revalidates one charity binding immediately before
// dispatch. The selected full name and full candidate predicate run again
// inside this single SELECT, so a model mismatch or donation/key/claim/
// endpoint/cache death between selection and dispatch fails closed as
// ErrNotFound without dialing or decrypting anything.
func (s *Store) GetCharityForwardTarget(ctx context.Context, bindingID int64, fullName string, now int64) (CharityForwardTarget, error) {
	if bindingID <= 0 || fullName == "" {
		return CharityForwardTarget{}, ErrNotFound
	}
	var (
		target          CharityForwardTarget
		baseURL         sql.NullString
		ciphertext      sql.NullString
		forceStoreFalse int
	)
	err := s.db.QueryRowContext(ctx, `
SELECT b.id, b.donation_key_id, d.user_id, e.id, ek.id, e.connector_type, ek.force_store_false,
       CASE WHEN length(CAST(e.base_url AS BLOB)) BETWEEN 1 AND ? THEN e.base_url END,
       b.upstream_model_id,
       CASE WHEN length(CAST(ek.encrypted_secret AS BLOB)) BETWEEN 1 AND ? THEN ek.encrypted_secret END
`+charityCandidatePredicate+`
  AND b.id = ?`,
		maxStoredEndpointBaseURLBytes, maxEndpointCredentialEnvelopeBytes, fullName, now, bindingID).
		Scan(&target.BindingID, &target.DonationKeyID, &target.DonorUserID,
			&target.EndpointID, &target.EndpointKeyID, &target.ConnectorType,
			&forceStoreFalse,
			&baseURL, &target.UpstreamModelID, &ciphertext)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CharityForwardTarget{}, ErrNotFound
		}
		return CharityForwardTarget{}, fmt.Errorf("read charity forward target: %w", err)
	}
	if !baseURL.Valid || !ciphertext.Valid {
		return CharityForwardTarget{}, ErrEndpointCredentialUnavailable
	}
	target.BaseURL = baseURL.String
	target.ForceStoreFalse = forceStoreFalse != 0
	target.ForwardTarget.BindingID = target.BindingID
	target.encryptedSecret = ciphertext.String
	return target, nil
}

// UserCharityModel is the user-station price/availability projection of one
// enabled charity model (frozen §6.3): original and currently discounted user
// prices, the independent donor-reward prices, the discount window, the
// last-100 protocol-success ring buffer, and server-resolved availability.
// It never carries a donated-key id or base URL: consumers cannot learn
// donated resources from the price table.
type UserCharityModel struct {
	ID               int64
	Provider         string
	Model            string
	FullName         string
	PricingMode      string
	FlattenToolCalls bool

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
	DiscountEnabled bool
	DiscountStartAt *int64
	DiscountEndAt   *int64

	SuccessSamples int
	SuccessCount   int

	Available bool

	// Current* are the effective user prices after the discount that is valid
	// at read time (equal to the originals when no discount applies). They are
	// display projections only; routing and billing always recompute
	// server-side at reservation time.
	CurrentRequestUserPrice    int64
	CurrentUncachedUserPrice   int64
	CurrentCacheWriteUserPrice int64
	CurrentCacheReadUserPrice  int64
	CurrentOutputUserPrice     int64
}

const userCharityModelSelectSQL = `
	SELECT cm.id, cm.provider, cm.model, cm.full_name, cm.pricing_mode, cm.flatten_tool_calls,
       cm.request_user_price, cm.request_donor_reward,
       cm.uncached_user_price, cm.cache_write_user_price, cm.cache_read_user_price, cm.output_user_price,
       cm.uncached_donor_reward, cm.cache_write_donor_reward, cm.cache_read_donor_reward, cm.output_donor_reward,
       cm.discount_percent, cm.discount_enabled, cm.discount_start_at, cm.discount_end_at,
       COALESCE(st.sample_count, 0), COALESCE(st.success_count, 0),
       EXISTS (
         SELECT 1 ` + charityCandidatePredicate + `
           AND b.charity_model_id = cm.id
       )
FROM charity_models cm
LEFT JOIN charity_model_stats st ON st.model_id = cm.id
WHERE cm.enabled = 1`

// ListUserCharityModels returns the enabled charity models with their price
// and availability projections while the authoritative site switch is on; a
// disabled site yields an empty list, never an error (frozen §6.3). Bounded,
// fail closed on limit violations.
func (s *Store) ListUserCharityModels(ctx context.Context, now int64, limit int) ([]UserCharityModel, error) {
	if limit <= 0 {
		return nil, ErrForwardProjectionLimit
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list user charity models: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	enabled, err := readCharityEnabledTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return []UserCharityModel{}, nil
	}
	rows, err := tx.QueryContext(ctx, userCharityModelSelectSQL+` ORDER BY cm.id LIMIT ?`, now, now, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list user charity models: query: %w", err)
	}
	out := make([]UserCharityModel, 0, min(limit, 32))
	for rows.Next() {
		if len(out) == limit {
			rows.Close()
			return nil, ErrForwardProjectionLimit
		}
		var (
			m              UserCharityModel
			startAt, endAt sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.Provider, &m.Model, &m.FullName, &m.PricingMode, &m.FlattenToolCalls,
			&m.RequestUserPrice, &m.RequestDonorReward,
			&m.UncachedUserPrice, &m.CacheWriteUserPrice, &m.CacheReadUserPrice, &m.OutputUserPrice,
			&m.UncachedDonorReward, &m.CacheWriteDonorReward, &m.CacheReadDonorReward, &m.OutputDonorReward,
			&m.DiscountPercent, &m.DiscountEnabled, &startAt, &endAt,
			&m.SuccessSamples, &m.SuccessCount,
			&m.Available); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list user charity models: scan: %w", err)
		}
		if startAt.Valid {
			v := startAt.Int64
			m.DiscountStartAt = &v
		}
		if endAt.Valid {
			v := endAt.Int64
			m.DiscountEndAt = &v
		}
		discount := (CharityModel{
			DiscountEnabled: m.DiscountEnabled,
			DiscountStartAt: m.DiscountStartAt,
			DiscountEndAt:   m.DiscountEndAt,
			DiscountPercent: m.DiscountPercent,
		}).EffectiveDiscountPercent(now)
		current := func(price int64) int64 {
			value, cerr := credits.ApplyDiscountPercent(price, discount)
			if cerr != nil {
				return price // fail closed to the undiscounted display value
			}
			return value
		}
		m.CurrentRequestUserPrice = current(m.RequestUserPrice)
		m.CurrentUncachedUserPrice = current(m.UncachedUserPrice)
		m.CurrentCacheWriteUserPrice = current(m.CacheWriteUserPrice)
		m.CurrentCacheReadUserPrice = current(m.CacheReadUserPrice)
		m.CurrentOutputUserPrice = current(m.OutputUserPrice)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("list user charity models: iterate: %w", err)
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("list user charity models: commit: %w", err)
	}
	return out, nil
}
