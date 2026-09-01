package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	MaxAvailableModels   = 1000
	MaxRuntimeCandidates = 100
)

// Preflight performs only candidate-free logical, policy, content, caller,
// and credit admission. In particular it does not materialize donation
// expiry, inspect a binding, health state, or three-dimensional key capacity.
func (s *Service) Preflight(ctx context.Context, userID int64, fullName string, request *openai.ChatRequest, decisionNow int64) (RuntimePreflight, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 || request == nil ||
		fullName == "" || fullName != request.Model || decisionNow < 0 || decisionNow > maxUnixSecond {
		return RuntimePreflight{}, ErrInvalidRequest
	}
	actual, err := request.CharityTextRuneCount()
	if err != nil {
		return RuntimePreflight{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RuntimePreflight{}, fmt.Errorf("charity routing: begin preflight: %w", err)
	}
	defer tx.Rollback()
	var admin, banned int
	var bannedUntil, suspendedUntil sql.NullInt64
	var gate, minimumText string
	err = tx.QueryRowContext(ctx, `SELECT u.is_admin,u.is_banned,u.banned_until,u.charity_suspended_until,
c.value,m.value
FROM users u
JOIN site_config c ON c.key='charity_enabled'
JOIN site_config m ON m.key='charity_min_chars'
WHERE u.id=?`, userID).Scan(&admin, &banned, &bannedUntil, &suspendedUntil, &gate, &minimumText)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimePreflight{}, ErrUnauthorized
	}
	if err != nil {
		return RuntimePreflight{}, fmt.Errorf("charity routing: read preflight caller: %w", err)
	}
	if admin != 0 || banned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > decisionNow) {
		return RuntimePreflight{}, ErrUnauthorized
	}
	if gate != "0" && gate != "1" {
		return RuntimePreflight{}, ErrInvariant
	}
	if gate == "0" {
		return RuntimePreflight{}, ErrFeatureDisabled
	}
	if suspendedUntil.Valid && suspendedUntil.Int64 > decisionNow {
		return RuntimePreflight{}, ErrCharitySuspended
	}
	minimum, err := strconv.Atoi(minimumText)
	if err != nil || minimum < 0 || int64(minimum) > openai.MaxRequestBodyBytes {
		return RuntimePreflight{}, ErrInvariant
	}
	if actual < minimum {
		return RuntimePreflight{}, &ContentTooShortError{Actual: actual, Minimum: minimum}
	}

	var preflight RuntimePreflight
	var enabled, discount, discountEnabled int
	var pricingMode string
	var requestPrice int64
	var discountStart, discountEnd sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT id,provider,model,full_name,enabled,flatten_tool_calls,
pricing_mode,request_user_price,discount_percent,discount_enabled,discount_start_at,discount_end_at
FROM charity_models WHERE full_name=?`, fullName).Scan(&preflight.ModelID, &preflight.Provider, &preflight.Model,
		&preflight.FullName, &enabled, &preflight.FlattenToolCalls, &pricingMode, &requestPrice,
		&discount, &discountEnabled, &discountStart, &discountEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimePreflight{}, ErrNotFound
	}
	if err != nil {
		return RuntimePreflight{}, fmt.Errorf("charity routing: read preflight model: %w", err)
	}
	if enabled != 1 {
		return RuntimePreflight{}, ErrNotFound
	}
	activeDiscount := discount
	if discountEnabled != 1 || discountStart.Valid && decisionNow < discountStart.Int64 || discountEnd.Valid && decisionNow >= discountEnd.Int64 {
		activeDiscount = 100
	}
	if pricingMode == "per_request" {
		preflight.ReservedMilli, err = credits.ApplyDiscountPercent(requestPrice, activeDiscount)
		if err != nil {
			return RuntimePreflight{}, ErrInvariant
		}
	} else if pricingMode == "per_token" {
		var stored sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_token_reserve_milli'`).Scan(&stored); err != nil {
			return RuntimePreflight{}, fmt.Errorf("charity routing: read preflight token reserve: %w", err)
		}
		if !stored.Valid {
			return RuntimePreflight{}, ErrNotFound
		}
		preflight.ReservedMilli, err = strconv.ParseInt(stored.String, 10, 64)
		if err != nil || preflight.ReservedMilli < 1 || preflight.ReservedMilli > db.MaxMoneyMilli {
			return RuntimePreflight{}, ErrInvariant
		}
	} else {
		return RuntimePreflight{}, ErrInvariant
	}
	var balanceSign int
	var balanceMagnitude []byte
	if err := tx.QueryRowContext(ctx, `SELECT balance_sign,balance_mag FROM credit_accounts
WHERE kind='user' AND user_id=?`, userID).Scan(&balanceSign, &balanceMagnitude); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RuntimePreflight{}, ErrInvariant
		}
		return RuntimePreflight{}, fmt.Errorf("charity routing: read preflight balance: %w", err)
	}
	magnitude, err := db.DecodeU128(balanceMagnitude)
	if err != nil || balanceSign < -1 || balanceSign > 1 || balanceSign == 0 && magnitude.Big().Sign() != 0 || balanceSign != 0 && magnitude.Big().Sign() == 0 {
		return RuntimePreflight{}, ErrInvariant
	}
	if preflight.ReservedMilli > 0 && (balanceSign <= 0 || magnitude.Big().Cmp(big.NewInt(preflight.ReservedMilli)) < 0) {
		return RuntimePreflight{}, ErrInsufficientCredits
	}
	if err := tx.Commit(); err != nil {
		return RuntimePreflight{}, fmt.Errorf("charity routing: commit preflight: %w", err)
	}
	return preflight, nil
}

// ListAvailableModels returns the bounded safe model projection used by the
// public OpenAI list. Availability is evaluated through Snapshot, but neither
// donation/key identities nor candidate counts leave this method.
func (s *Service) ListAvailableModels(ctx context.Context, decisionNow int64, limit int) ([]AvailableModel, error) {
	if s == nil || s.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > MaxAvailableModels {
		return nil, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("charity routing: begin available model list: %w", err)
	}
	defer tx.Rollback()
	gate, err := capabilityGateTx(ctx, tx, "charity_enabled")
	if err != nil {
		return nil, err
	}
	if gate == "0" {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("charity routing: commit disabled model list: %w", err)
		}
		return []AvailableModel{}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,provider,full_name,created_at FROM charity_models
WHERE enabled=1 ORDER BY full_name,id LIMIT ?`, limit+1)
	if err != nil {
		return nil, fmt.Errorf("charity routing: read available model list: %w", err)
	}
	models := make([]AvailableModel, 0, limit)
	for rows.Next() {
		var model AvailableModel
		if err := rows.Scan(&model.ModelID, &model.Provider, &model.FullName, &model.CreatedAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("charity routing: scan available model list: %w", err)
		}
		models = append(models, model)
		if len(models) > limit {
			_ = rows.Close()
			return nil, ErrResourceLimit
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("charity routing: iterate available model list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("charity routing: close available model list: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("charity routing: commit available model list: %w", err)
	}
	available := make([]AvailableModel, 0, len(models))
	for _, model := range models {
		if _, err := s.Snapshot(ctx, model.ModelID, decisionNow); err == nil {
			available = append(available, model)
		} else if !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	sort.Slice(available, func(i, j int) bool {
		if available[i].FullName == available[j].FullName {
			return available[i].ModelID < available[j].ModelID
		}
		return available[i].FullName < available[j].FullName
	})
	return available, nil
}

// Snapshot returns the current credential-free candidate set. Every candidate
// passes the same physical, membership, expiry, suspension, catalog, binding,
// switch, and three-dimensional admission checks that claim repeats.
func (s *Service) Snapshot(ctx context.Context, modelID int64, decisionNow int64) (RuntimeSnapshot, error) {
	if s == nil || s.db == nil || ctx == nil || modelID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond {
		return RuntimeSnapshot{}, ErrInvalidRequest
	}
	var snapshot RuntimeSnapshot
	var enabled int
	var pricingMode string
	var requestPrice int64
	var discount, discountEnabled int
	var discountStart, discountEnd sql.NullInt64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: begin runtime snapshot: %w", err)
	}
	defer tx.Rollback()
	if err := s.donationState.MaterializeDueExpiriesTx(ctx, tx, decisionNow, 100); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: materialize runtime expiry: %w", err)
	}
	var gate string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_enabled'`).Scan(&gate); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: read runtime feature gate: %w", err)
	}
	if gate != "0" && gate != "1" {
		return RuntimeSnapshot{}, ErrInvariant
	}
	if gate == "0" {
		return RuntimeSnapshot{}, ErrNotFound
	}
	err = tx.QueryRowContext(ctx, `SELECT id,provider,model,full_name,enabled,flatten_tool_calls,
pricing_mode,request_user_price,discount_percent,discount_enabled,discount_start_at,discount_end_at
FROM charity_models WHERE id=?`, modelID).Scan(&snapshot.ModelID, &snapshot.Provider, &snapshot.Model,
		&snapshot.FullName, &enabled, &snapshot.FlattenToolCalls, &pricingMode, &requestPrice,
		&discount, &discountEnabled, &discountStart, &discountEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeSnapshot{}, ErrNotFound
	}
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: read runtime model: %w", err)
	}
	if enabled != 1 {
		return RuntimeSnapshot{}, ErrNotFound
	}
	activeDiscount := discount
	if discountEnabled != 1 || discountStart.Valid && decisionNow < discountStart.Int64 || discountEnd.Valid && decisionNow >= discountEnd.Int64 {
		activeDiscount = 100
	}
	if pricingMode == "per_request" {
		snapshot.ReservedMilli, err = credits.ApplyDiscountPercent(requestPrice, activeDiscount)
		if err != nil {
			return RuntimeSnapshot{}, ErrInvariant
		}
	} else if pricingMode == "per_token" {
		var stored string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_token_reserve_milli'`).Scan(&stored); err != nil {
			return RuntimeSnapshot{}, fmt.Errorf("charity routing: read token reserve: %w", err)
		}
		snapshot.ReservedMilli, err = strconv.ParseInt(stored, 10, 64)
		if err != nil || snapshot.ReservedMilli < 1 || snapshot.ReservedMilli > db.MaxMoneyMilli {
			return RuntimeSnapshot{}, ErrInvariant
		}
	} else {
		return RuntimeSnapshot{}, ErrInvariant
	}

	rows, err := tx.QueryContext(ctx, `SELECT b.donation_key_id,e.id,k.id,e.connector_type,e.base_url,b.upstream_model_id,
k.force_store_false,dk.token_reserve,
dk.price_limit_mag,dk.call_limit_mag,dk.token_limit_mag,
dk.price_used_mag,dk.price_reserved_mag,dk.calls_used,dk.calls_reserved,dk.tokens_used,dk.tokens_reserved
FROM charity_model_bindings b
JOIN donation_keys dk ON dk.id=b.donation_key_id
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
JOIN model_pair_catalog pc ON pc.endpoint_key_id=b.endpoint_key_id AND pc.normalized_model_id=b.upstream_model_id
WHERE b.charity_model_id=? AND d.status='approved' AND (d.expires_at IS NULL OR d.expires_at>?)
AND d.user_id IS NOT NULL AND dk.ended_at IS NULL AND dk.enabled=1 AND dk.failure_disabled=0
AND k.enabled=1 AND e.enabled=1 AND (pc.automatic_supports>0 OR pc.manual_supports>0)
AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)
ORDER BY b.ord,b.id LIMIT ?`, modelID, decisionNow, MaxRuntimeCandidates+1)
	if err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: read runtime candidates: %w", err)
	}
	defer rows.Close()
	snapshot.candidates = make([]RuntimeCandidate, 0)
	for rows.Next() {
		var candidate RuntimeCandidate
		var connector string
		var forceStore int
		var tokenReserve int64
		var priceLimit, callLimit, tokenLimit []byte
		var priceUsed, priceInflight, callsUsed, callsInflight, tokensUsed, tokensInflight []byte
		if err := rows.Scan(&candidate.DonationKeyID, &candidate.EndpointID, &candidate.EndpointKeyID,
			&connector, &candidate.CanonicalBaseURL, &candidate.UpstreamModelID, &forceStore, &tokenReserve,
			&priceLimit, &callLimit, &tokenLimit, &priceUsed, &priceInflight, &callsUsed, &callsInflight,
			&tokensUsed, &tokensInflight); err != nil {
			return RuntimeSnapshot{}, fmt.Errorf("charity routing: scan runtime candidate: %w", err)
		}
		candidate.ConnectorType = connectorcontract.Type(connector)
		if candidate.ConnectorType != connectorcontract.TypeOpenAICompatible && candidate.ConnectorType != connectorcontract.TypeAnthropicCompatible {
			return RuntimeSnapshot{}, ErrInvariant
		}
		priceReserve := snapshot.ReservedMilli
		if pricingMode == "per_request" {
			priceReserve = requestPrice
		}
		eligible, err := capacityAllows(priceLimit, priceUsed, priceInflight, big.NewInt(priceReserve))
		if err == nil && eligible {
			eligible, err = capacityAllows(callLimit, callsUsed, callsInflight, big.NewInt(1))
		}
		if err == nil && eligible {
			eligible, err = capacityAllows(tokenLimit, tokensUsed, tokensInflight, big.NewInt(tokenReserve))
		}
		if err != nil {
			return RuntimeSnapshot{}, err
		}
		if !eligible {
			continue
		}
		candidate.Policy.ForceStoreFalse = forceStore == 1
		candidate.Policy.FlattenToolCalls = snapshot.FlattenToolCalls
		snapshot.candidates = append(snapshot.candidates, candidate)
		if len(snapshot.candidates) > MaxRuntimeCandidates {
			return RuntimeSnapshot{}, ErrResourceLimit
		}
	}
	if err := rows.Err(); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: iterate runtime candidates: %w", err)
	}
	if len(snapshot.candidates) == 0 {
		if err := tx.Commit(); err != nil {
			return RuntimeSnapshot{}, fmt.Errorf("charity routing: commit empty runtime snapshot: %w", err)
		}
		return RuntimeSnapshot{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return RuntimeSnapshot{}, fmt.Errorf("charity routing: commit runtime snapshot: %w", err)
	}
	return snapshot, nil
}

func capacityAllows(limitBlob, usedBlob, reservedBlob []byte, increment *big.Int) (bool, error) {
	if limitBlob == nil {
		return true, nil
	}
	limit, err := db.DecodeU128(limitBlob)
	if err != nil {
		return false, ErrInvariant
	}
	used, err := db.DecodeU128(usedBlob)
	if err != nil {
		return false, ErrInvariant
	}
	reserved, err := db.DecodeU128(reservedBlob)
	if err != nil {
		return false, ErrInvariant
	}
	current := new(big.Int).Add(used.Big(), reserved.Big())
	if current.Cmp(limit.Big()) >= 0 {
		return false, nil
	}
	return new(big.Int).Add(current, increment).Cmp(limit.Big()) <= 0, nil
}

func (s *Service) Capability(ctx context.Context, decisionNow int64) (Capability, error) {
	if s == nil || s.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond {
		return Capability{}, ErrInvalidRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Capability{}, fmt.Errorf("charity routing: begin capability snapshot: %w", err)
	}
	defer tx.Rollback()
	charityGate, err := capabilityGateTx(ctx, tx, "charity_enabled")
	if err != nil {
		return Capability{}, err
	}
	donationGate, err := capabilityGateTx(ctx, tx, "donation_accept_enabled")
	if err != nil {
		return Capability{}, err
	}
	if charityGate == "0" && donationGate == "1" {
		return Capability{}, ErrInvariant
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,provider,model,full_name FROM charity_models
WHERE enabled=1 ORDER BY id`)
	if err != nil {
		return Capability{}, fmt.Errorf("charity routing: read capability models: %w", err)
	}
	models := make([]CapabilityModel, 0)
	modelIDs := make([]int64, 0)
	for rows.Next() {
		var model CapabilityModel
		var id int64
		if err := rows.Scan(&id, &model.Provider, &model.Model, &model.FullName); err != nil {
			_ = rows.Close()
			return Capability{}, fmt.Errorf("charity routing: scan capability model: %w", err)
		}
		model.ID = strconv.FormatInt(id, 10)
		models = append(models, model)
		modelIDs = append(modelIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Capability{}, fmt.Errorf("charity routing: iterate capability models: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Capability{}, fmt.Errorf("charity routing: close capability models: %w", err)
	}
	// Snapshot opens its own transaction and can materialize donation expiry.
	// Commit the complete gate/model fact snapshot first so a one-connection
	// production pool cannot self-deadlock and later candidate changes cannot
	// rewrite the intake fact observed above.
	if err := tx.Commit(); err != nil {
		return Capability{}, fmt.Errorf("charity routing: commit capability snapshot: %w", err)
	}
	donationIntake := "closed"
	if charityGate == "1" && donationGate == "1" {
		donationIntake = "open"
	}
	if charityGate == "0" {
		return Capability{State: "feature_disabled", Models: []CapabilityModel{}, DonationIntake: donationIntake}, nil
	}
	if len(models) == 0 {
		return Capability{State: "no_models", Models: []CapabilityModel{}, DonationIntake: donationIntake}, nil
	}
	available := make([]CapabilityModel, 0, len(models))
	for index, id := range modelIDs {
		if _, err := s.Snapshot(ctx, id, decisionNow); err == nil {
			available = append(available, models[index])
		} else if !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrNotFound) {
			return Capability{}, err
		}
	}
	if len(available) == 0 {
		return Capability{State: "no_candidates", Models: []CapabilityModel{}, DonationIntake: donationIntake}, nil
	}
	return Capability{State: "available", Models: available, DonationIntake: donationIntake}, nil
}

func capabilityGateTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvariant
		}
		return "", fmt.Errorf("charity routing: read capability gate: %w", err)
	}
	if value != "0" && value != "1" {
		return "", ErrInvariant
	}
	return value, nil
}
