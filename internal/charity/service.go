// Package charity owns the transaction-local economic state for charity
// requests. It is deliberately downstream of claim: it never manufactures a
// dispatch claim, reads a credential, or writes a ledger operation directly.
package charity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	maxUnixSecond       = int64(253402300799)
	terminalRetention   = int64(400 * 24 * 60 * 60)
	failureDisableCount = int64(10)
)

// EndpointKeyDeletionOwner is the donation-side transaction primitive. The
// aggregate Service exposes it as resources.EndpointKeyDeletionHook so root
// composition can share one transactional implementation.
type EndpointKeyDeletionOwner interface {
	PrepareEndpointKeyDeletion(context.Context, *sql.Tx, int64, []int64, int64) error
	MaterializeExpiryTx(context.Context, *sql.Tx, int64, int64) (bool, error)
}

type Config struct {
	Store       *db.Store
	KeyDeletion EndpointKeyDeletionOwner
	Now         func() time.Time
}

type Service struct {
	db          *sql.DB
	keyDeletion EndpointKeyDeletionOwner
	now         func() time.Time
}

var _ claim.Charity = (*Service)(nil)
var _ resources.EndpointKeyDeletionHook = (*Service)(nil)

func New(config Config) (*Service, error) {
	if config.Store == nil || config.Store.DB() == nil || nilInterface(config.KeyDeletion) {
		return nil, errors.New("charity: store and donation deletion owner are required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{db: config.Store.DB(), keyDeletion: config.KeyDeletion, now: config.Now}, nil
}

func (s *Service) PrepareEndpointKeyDeletion(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	keyIDs []int64,
	decisionNow int64,
) error {
	if s == nil || nilInterface(s.keyDeletion) {
		return claim.ErrDependencyUnavailable
	}
	return s.keyDeletion.PrepareEndpointKeyDeletion(ctx, tx, ownerUserID, keyIDs, decisionNow)
}

type frozenPricing struct {
	mode                        string
	discount                    int
	requestUser, requestReward  int64
	uncachedUser, writeUser     int64
	readUser, outputUser        int64
	uncachedReward, writeReward int64
	readReward, outputReward    int64
	tokenReserve                int64
}

func (s *Service) AcceptRequest(ctx context.Context, tx *sql.Tx, input claim.CharityAcceptance) error {
	if s == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(input.RequestID, "req_") ||
		input.UserID <= 0 || input.CharityModelID <= 0 || input.AttemptLimit < 1 ||
		input.AttemptLimit > claim.MaxAttempts || !validMoney(input.ReservedMilli) ||
		!validTime(input.AcceptedAt) || len([]byte(input.ModelSnapshot)) > claim.MaxModelSnapshotBytes {
		return claim.ErrInvalidInput
	}

	pricing, err := readAcceptancePricing(ctx, tx, input.CharityModelID, input.AcceptedAt)
	if err != nil {
		return err
	}
	expectedReserve := pricing.tokenReserve
	if pricing.mode == "per_request" {
		expectedReserve, err = credits.ApplyDiscountPercent(pricing.requestUser, pricing.discount)
		if err != nil {
			return claim.ErrInvariant
		}
	}
	if input.ReservedMilli != expectedReserve {
		return claim.ErrConflict
	}

	zero := db.EncodeU128(db.U128{})
	_, err = tx.ExecContext(ctx, `INSERT INTO charity_reservations(
logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,discount_percent,
request_user_price_milli,request_donor_reward_milli,
uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,
uncached_donor_reward_milli,cache_write_donor_reward_milli,cache_read_donor_reward_milli,output_donor_reward_milli,
token_reserve_milli,user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,
created_at,updated_at)
VALUES(?,?,?,?,'reserved',?,
?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,?,?,?)`,
		input.RequestID, input.UserID, input.CharityModelID, input.ModelSnapshot, pricing.mode, pricing.discount,
		pricing.requestUser, pricing.requestReward,
		pricing.uncachedUser, pricing.writeUser, pricing.readUser, pricing.outputUser,
		pricing.uncachedReward, pricing.writeReward, pricing.readReward, pricing.outputReward,
		pricing.tokenReserve, input.ReservedMilli, zero, input.AcceptedAt, input.AcceptedAt)
	if err != nil {
		return fmt.Errorf("charity: persist request reservation: %w", err)
	}
	return nil
}

func readAcceptancePricing(ctx context.Context, tx *sql.Tx, modelID, at int64) (frozenPricing, error) {
	var p frozenPricing
	var gate string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_enabled'`).Scan(&gate); err != nil {
		return frozenPricing{}, fmt.Errorf("charity: read feature gate: %w", err)
	}
	if gate != "0" && gate != "1" {
		return frozenPricing{}, claim.ErrInvariant
	}
	if gate == "0" {
		return frozenPricing{}, claim.ErrNotFound
	}
	var enabled, discountEnabled int
	var start, end sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT pricing_mode,enabled,discount_percent,discount_enabled,
discount_start_at,discount_end_at,request_user_price,request_donor_reward,
uncached_user_price,cache_write_user_price,cache_read_user_price,output_user_price,
uncached_donor_reward,cache_write_donor_reward,cache_read_donor_reward,output_donor_reward
FROM charity_models WHERE id=?`, modelID).Scan(
		&p.mode, &enabled, &p.discount, &discountEnabled, &start, &end,
		&p.requestUser, &p.requestReward, &p.uncachedUser, &p.writeUser, &p.readUser, &p.outputUser,
		&p.uncachedReward, &p.writeReward, &p.readReward, &p.outputReward)
	if errors.Is(err, sql.ErrNoRows) {
		return frozenPricing{}, claim.ErrNotFound
	}
	if err != nil {
		return frozenPricing{}, fmt.Errorf("charity: read model pricing: %w", err)
	}
	if enabled != 1 || (p.mode != "per_request" && p.mode != "per_token") || p.discount < 0 || p.discount > 100 {
		return frozenPricing{}, claim.ErrNotFound
	}
	if discountEnabled != 1 || start.Valid && at < start.Int64 || end.Valid && at >= end.Int64 {
		p.discount = 100
	}
	if p.mode == "per_request" {
		p.uncachedUser, p.writeUser, p.readUser, p.outputUser = 0, 0, 0, 0
		p.uncachedReward, p.writeReward, p.readReward, p.outputReward = 0, 0, 0, 0
		return p, nil
	}
	p.requestUser, p.requestReward = 0, 0
	var tokenReserve string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_token_reserve_milli'`).Scan(&tokenReserve); err != nil {
		return frozenPricing{}, fmt.Errorf("charity: read token reservation: %w", err)
	}
	value, err := strconv.ParseInt(tokenReserve, 10, 64)
	if err != nil || value < 1 || value > claim.MaxMoneyMilli {
		return frozenPricing{}, claim.ErrInvariant
	}
	p.tokenReserve = value
	return p, nil
}

type keyReservation struct {
	donationID, donationKeyID, endpointKeyID, endpointID, receiverUserID int64
	modelID, modelEnabled, keyEnabled, failureDisabled                   int64
	endpointEnabled, physicalKeyEnabled                                  int64
	expiresAt                                                            sql.NullInt64
	status, endedReason                                                  string
	priceLimit, callLimit, tokenLimit                                    []byte
	priceUsed, priceReserved, callsUsed, callsReserved                   []byte
	tokensUsed, tokensReserved, generation, nextClaim                    []byte
	tokenReserve                                                         int64
	pricing                                                              frozenPricing
}

func (s *Service) Claim(ctx context.Context, tx *sql.Tx, input claim.CharityClaimInput) (claim.CharityReservation, error) {
	if s == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(input.RequestID, "req_") ||
		!db.ValidateOpaqueID(input.ClaimID, "clm_") || input.ActorUserID <= 0 ||
		input.AttemptSeq < 1 || input.AttemptSeq > claim.MaxAttempts || input.DonationKeyID <= 0 ||
		input.EndpointID <= 0 || input.EndpointKeyID <= 0 || !validUpstreamModelID(input.UpstreamModelID) ||
		!validTime(input.ClaimedAt) {
		return claim.CharityReservation{}, claim.ErrInvalidInput
	}
	var gate string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='charity_enabled'`).Scan(&gate); err != nil {
		return claim.CharityReservation{}, fmt.Errorf("charity: read claim feature gate: %w", err)
	}
	if gate != "0" && gate != "1" {
		return claim.CharityReservation{}, claim.ErrInvariant
	}
	if gate == "0" {
		return claim.CharityReservation{}, claim.ErrNotFound
	}

	var donationID int64
	if err := tx.QueryRowContext(ctx, `SELECT donation_id FROM donation_keys WHERE id=?`, input.DonationKeyID).Scan(&donationID); errors.Is(err, sql.ErrNoRows) {
		return claim.CharityReservation{}, claim.ErrNotFound
	} else if err != nil {
		return claim.CharityReservation{}, fmt.Errorf("charity: locate claim donation: %w", err)
	}
	expired, err := s.keyDeletion.MaterializeExpiryTx(ctx, tx, donationID, input.ClaimedAt)
	if err != nil {
		return claim.CharityReservation{}, fmt.Errorf("charity: materialize claim expiry: %w", err)
	}
	if expired {
		return claim.CharityReservation{}, claim.ErrNotFound
	}

	row, err := readClaimKey(ctx, tx, input)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	if row.status != "approved" || row.endedReason != "" || row.keyEnabled != 1 || row.failureDisabled != 0 ||
		row.modelEnabled != 1 || row.endpointEnabled != 1 || row.physicalKeyEnabled != 1 ||
		row.expiresAt.Valid && input.ClaimedAt >= row.expiresAt.Int64 {
		return claim.CharityReservation{}, claim.ErrNotFound
	}

	priceReserve := row.pricing.tokenReserve
	frozenPrice, frozenReward := priceReserve, int64(0)
	if row.pricing.mode == "per_request" {
		priceReserve = row.pricing.requestUser
		frozenPrice, frozenReward = row.pricing.requestUser, row.pricing.requestReward
	}

	priceUsed, err := decodeU128(row.priceUsed)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	priceInflight, err := decodeU128(row.priceReserved)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	callsUsed, err := decodeU128(row.callsUsed)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	callsInflight, err := decodeU128(row.callsReserved)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	tokensUsed, err := decodeU128(row.tokensUsed)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	tokensInflight, err := decodeU128(row.tokensReserved)
	if err != nil {
		return claim.CharityReservation{}, err
	}
	generation, err := decodeU128(row.generation)
	if err != nil || generation.Big().Sign() <= 0 || generation.Big().BitLen() > 63 {
		return claim.CharityReservation{}, claim.ErrInvariant
	}
	claimSeq, err := decodeU128(row.nextClaim)
	if err != nil {
		return claim.CharityReservation{}, err
	}

	newPrice, err := reserveDimension(priceUsed, priceInflight, row.priceLimit, big.NewInt(priceReserve))
	if err != nil {
		return claim.CharityReservation{}, err
	}
	newCalls, err := reserveDimension(callsUsed, callsInflight, row.callLimit, big.NewInt(1))
	if err != nil {
		return claim.CharityReservation{}, err
	}
	newTokens, err := reserveDimension(tokensUsed, tokensInflight, row.tokenLimit, big.NewInt(row.tokenReserve))
	if err != nil {
		return claim.CharityReservation{}, err
	}
	nextClaim, err := addU128(claimSeq, big.NewInt(1))
	if err != nil {
		return claim.CharityReservation{}, err
	}

	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET
price_reserved_mag=?,calls_reserved=?,tokens_reserved=?,next_claim_seq=?,updated_at=?
WHERE id=? AND endpoint_key_id=? AND ended_at IS NULL
  AND price_reserved_mag=? AND calls_reserved=? AND tokens_reserved=? AND next_claim_seq=?`,
		db.EncodeU128(newPrice), db.EncodeU128(newCalls), db.EncodeU128(newTokens), db.EncodeU128(nextClaim), input.ClaimedAt,
		row.donationKeyID, row.endpointKeyID, row.priceReserved, row.callsReserved, row.tokensReserved, row.nextClaim)
	if err != nil {
		return claim.CharityReservation{}, fmt.Errorf("charity: reserve key capacity: %w", err)
	}
	if err := requireOne(result); err != nil {
		return claim.CharityReservation{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_usage_reservations(
claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at)
VALUES(?,?,?,?,?,?,?,'reserved',?)`, input.ClaimID, row.donationKeyID, db.EncodeU128(generation),
		db.EncodeU128(claimSeq), priceReserve, 1, row.tokenReserve, input.ClaimedAt); err != nil {
		return claim.CharityReservation{}, fmt.Errorf("charity: persist claim reservation: %w", err)
	}

	return claim.CharityReservation{
		DonationKeyID: row.donationKeyID, StreakGeneration: generation.Big().Int64(),
		FrozenPriceMilli: frozenPrice, FrozenRewardMilli: frozenReward,
		ReceiverUserID: row.receiverUserID, ReservedPriceMilli: priceReserve,
		ReservedCalls: 1, ReservedTokens: row.tokenReserve,
	}, nil
}

func readClaimKey(ctx context.Context, tx *sql.Tx, input claim.CharityClaimInput) (keyReservation, error) {
	var row keyReservation
	var reservationState, requestState, requestRoute string
	var suspended int
	err := tx.QueryRowContext(ctx, `SELECT
d.id,d.status,dk.expires_at,COALESCE(d.user_id,0),dk.id,COALESCE(dk.endpoint_key_id,0),e.id,
dk.enabled,dk.failure_disabled,COALESCE(dk.ended_reason,''),dk.price_limit_mag,dk.call_limit_mag,dk.token_limit_mag,
dk.price_used_mag,dk.price_reserved_mag,dk.calls_used,dk.calls_reserved,dk.tokens_used,dk.tokens_reserved,
dk.streak_generation,dk.next_claim_seq,dk.token_reserve,e.enabled,k.enabled,
cr.charity_model_id,cm.enabled,cr.state,lr.state,lr.route_kind,
cr.pricing_mode,cr.discount_percent,cr.request_user_price_milli,cr.request_donor_reward_milli,
cr.uncached_user_price_milli,cr.cache_write_user_price_milli,cr.cache_read_user_price_milli,cr.output_user_price_milli,
cr.uncached_donor_reward_milli,cr.cache_write_donor_reward_milli,cr.cache_read_donor_reward_milli,cr.output_donor_reward_milli,
cr.token_reserve_milli,
EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
FROM charity_reservations cr
JOIN logical_requests lr ON lr.id=cr.logical_request_id
JOIN donation_keys dk ON dk.id=?
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=dk.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
JOIN charity_models cm ON cm.id=cr.charity_model_id
JOIN charity_model_bindings b ON b.charity_model_id=cm.id AND b.donation_key_id=dk.id
 AND b.endpoint_key_id=k.id AND b.upstream_model_id=?
JOIN model_pair_catalog pc ON pc.endpoint_key_id=b.endpoint_key_id
 AND pc.normalized_model_id=b.upstream_model_id AND pc.normalized_model_id=?
WHERE cr.logical_request_id=? AND cr.user_id=? AND lr.user_id=? AND e.id=? AND k.id=?
  AND (pc.automatic_supports>0 OR pc.manual_supports>0)
LIMIT 1`, input.DonationKeyID, input.UpstreamModelID, input.UpstreamModelID,
		input.RequestID, input.ActorUserID, input.ActorUserID,
		input.EndpointID, input.EndpointKeyID).Scan(
		&row.donationID, &row.status, &row.expiresAt, &row.receiverUserID, &row.donationKeyID,
		&row.endpointKeyID, &row.endpointID, &row.keyEnabled, &row.failureDisabled, &row.endedReason,
		&row.priceLimit, &row.callLimit, &row.tokenLimit, &row.priceUsed, &row.priceReserved,
		&row.callsUsed, &row.callsReserved, &row.tokensUsed, &row.tokensReserved,
		&row.generation, &row.nextClaim, &row.tokenReserve, &row.endpointEnabled, &row.physicalKeyEnabled,
		&row.modelID, &row.modelEnabled, &reservationState, &requestState, &requestRoute,
		&row.pricing.mode, &row.pricing.discount, &row.pricing.requestUser, &row.pricing.requestReward,
		&row.pricing.uncachedUser, &row.pricing.writeUser, &row.pricing.readUser, &row.pricing.outputUser,
		&row.pricing.uncachedReward, &row.pricing.writeReward, &row.pricing.readReward, &row.pricing.outputReward,
		&row.pricing.tokenReserve, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return keyReservation{}, claim.ErrNotFound
	}
	if err != nil {
		return keyReservation{}, fmt.Errorf("charity: revalidate claim candidate: %w", err)
	}
	if row.receiverUserID <= 0 || row.donationKeyID != input.DonationKeyID || row.endpointKeyID != input.EndpointKeyID ||
		row.endpointID != input.EndpointID || row.modelID <= 0 || suspended != 0 ||
		reservationState != "reserved" && reservationState != "dispatched" ||
		requestState != "accepted" && requestState != "running" || requestRoute != string(claim.RouteCharityChat) {
		return keyReservation{}, claim.ErrNotFound
	}
	return row, nil
}

func validUpstreamModelID(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 512 {
		return false
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func reserveDimension(used, reserved db.U128, limitBlob []byte, increment *big.Int) (db.U128, error) {
	if increment == nil || increment.Sign() < 0 {
		return db.U128{}, claim.ErrInvariant
	}
	current := new(big.Int).Add(used.Big(), reserved.Big())
	if limitBlob != nil {
		limit, err := db.DecodeU128(limitBlob)
		if err != nil {
			return db.U128{}, claim.ErrInvariant
		}
		if current.Cmp(limit.Big()) >= 0 || new(big.Int).Add(current, increment).Cmp(limit.Big()) > 0 {
			return db.U128{}, claim.ErrNotFound
		}
	}
	return addU128(reserved, increment)
}

func (s *Service) ReleaseUndispatched(ctx context.Context, tx *sql.Tx, input claim.CharityRelease) error {
	if s == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(input.RequestID, "req_") ||
		!db.ValidateOpaqueID(input.ClaimID, "clm_") || !validTime(input.ReleasedAt) {
		return claim.ErrInvalidInput
	}
	row, err := readUsageReservation(ctx, tx, input.RequestID, input.ClaimID)
	if err != nil {
		return err
	}
	if input.DonationKeyID != nil && (row.keyID == nil || *input.DonationKeyID != *row.keyID) {
		return claim.ErrConflict
	}
	if row.state == "released" {
		return nil
	}
	if row.state != "reserved" {
		return claim.ErrConflict
	}
	if row.keyID != nil {
		if err := releaseKeyCapacity(ctx, tx, *row.keyID, row.priceReserved, row.callsReserved, row.tokensReserved, input.ReleasedAt); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_usage_reservations
SET state='released',finalized_at=? WHERE claim_id=? AND state='reserved'`, input.ReleasedAt, input.ClaimID)
	if err != nil {
		return fmt.Errorf("charity: release claim reservation: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if row.keyID != nil {
		return foldStreak(ctx, tx, *row.keyID, row.generation, input.ReleasedAt)
	}
	return nil
}

type usageReservation struct {
	keyID           *int64
	state           string
	generation, seq db.U128
	priceReserved   int64
	callsReserved   int
	tokensReserved  int64
}

func readUsageReservation(ctx context.Context, tx *sql.Tx, requestID, claimID string) (usageReservation, error) {
	var row usageReservation
	var key sql.NullInt64
	var generation, seq []byte
	err := tx.QueryRowContext(ctx, `SELECT u.donation_key_id,u.state,u.streak_generation,u.claim_seq,
u.price_reserved_milli,u.calls_reserved,u.tokens_reserved
FROM donation_usage_reservations u
JOIN dispatch_claims c ON c.id=u.claim_id
WHERE u.claim_id=? AND c.logical_request_id=?`, claimID, requestID).Scan(
		&key, &row.state, &generation, &seq, &row.priceReserved, &row.callsReserved, &row.tokensReserved)
	if errors.Is(err, sql.ErrNoRows) {
		return usageReservation{}, claim.ErrNotFound
	}
	if err != nil {
		return usageReservation{}, fmt.Errorf("charity: read claim reservation: %w", err)
	}
	var decodeErr error
	row.generation, decodeErr = db.DecodeU128(generation)
	if decodeErr == nil {
		row.seq, decodeErr = db.DecodeU128(seq)
	}
	if decodeErr != nil {
		return usageReservation{}, claim.ErrInvariant
	}
	if key.Valid {
		value := key.Int64
		row.keyID = &value
	}
	return row, nil
}

func releaseKeyCapacity(ctx context.Context, tx *sql.Tx, keyID, price int64, calls int, tokens int64, at int64) error {
	var priceBlob, callsBlob, tokensBlob []byte
	if err := tx.QueryRowContext(ctx, `SELECT price_reserved_mag,calls_reserved,tokens_reserved
FROM donation_keys WHERE id=?`, keyID).Scan(&priceBlob, &callsBlob, &tokensBlob); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return claim.ErrInvariant
		}
		return fmt.Errorf("charity: read key capacity: %w", err)
	}
	p, err := decodeU128(priceBlob)
	if err != nil {
		return err
	}
	c, err := decodeU128(callsBlob)
	if err != nil {
		return err
	}
	t, err := decodeU128(tokensBlob)
	if err != nil {
		return err
	}
	p, err = subU128(p, big.NewInt(price))
	if err != nil {
		return err
	}
	c, err = subU128(c, big.NewInt(int64(calls)))
	if err != nil {
		return err
	}
	t, err = subU128(t, big.NewInt(tokens))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET price_reserved_mag=?,calls_reserved=?,tokens_reserved=?,updated_at=?
WHERE id=? AND price_reserved_mag=? AND calls_reserved=? AND tokens_reserved=?`,
		db.EncodeU128(p), db.EncodeU128(c), db.EncodeU128(t), at, keyID, priceBlob, callsBlob, tokensBlob)
	if err != nil {
		return fmt.Errorf("charity: release key capacity: %w", err)
	}
	return requireOne(result)
}

func (s *Service) PrepareAttempt(ctx context.Context, tx *sql.Tx, input claim.CharityAttemptInput) (claim.CharityActual, error) {
	if s == nil || ctx == nil || tx == nil || !validAttemptInput(input) {
		return claim.CharityActual{}, claim.ErrInvalidInput
	}
	return prepareAttempt(ctx, tx, input)
}

func prepareAttempt(ctx context.Context, tx *sql.Tx, input claim.CharityAttemptInput) (claim.CharityActual, error) {
	var state, mode string
	var key, receiver sql.NullInt64
	var requestUser, requestReward int64
	var uncachedUser, writeUser, readUser, outputUser int64
	var uncachedReward, writeReward, readReward, outputReward int64
	var reservedTokens int64
	err := tx.QueryRowContext(ctx, `SELECT u.state,u.donation_key_id,c.receiver_user_id,u.tokens_reserved,
cr.pricing_mode,cr.request_user_price_milli,cr.request_donor_reward_milli,
cr.uncached_user_price_milli,cr.cache_write_user_price_milli,cr.cache_read_user_price_milli,cr.output_user_price_milli,
cr.uncached_donor_reward_milli,cr.cache_write_donor_reward_milli,cr.cache_read_donor_reward_milli,cr.output_donor_reward_milli
FROM donation_usage_reservations u
JOIN dispatch_claims c ON c.id=u.claim_id
JOIN charity_reservations cr ON cr.logical_request_id=c.logical_request_id
WHERE u.claim_id=? AND c.logical_request_id=?`, input.ClaimID, input.RequestID).Scan(
		&state, &key, &receiver, &reservedTokens, &mode, &requestUser, &requestReward,
		&uncachedUser, &writeUser, &readUser, &outputUser,
		&uncachedReward, &writeReward, &readReward, &outputReward)
	if errors.Is(err, sql.ErrNoRows) {
		return claim.CharityActual{}, claim.ErrNotFound
	}
	if err != nil {
		return claim.CharityActual{}, fmt.Errorf("charity: read frozen attempt pricing: %w", err)
	}
	if input.ReceiverUserID == nil && receiver.Valid || input.ReceiverUserID != nil && (!receiver.Valid || receiver.Int64 != *input.ReceiverUserID) {
		return claim.CharityActual{}, claim.ErrConflict
	}
	if state != "reserved" || input.DonationKeyID != nil && (!key.Valid || key.Int64 != *input.DonationKeyID) {
		return claim.CharityActual{}, claim.ErrConflict
	}
	if !input.ResponseStarted {
		return claim.CharityActual{}, nil
	}

	if mode == "per_request" {
		reward := requestReward
		if input.SuppressReward || input.UsageUnknown {
			reward = 0
		}
		return claim.CharityActual{PriceMilli: requestUser, RewardMilli: reward}, nil
	}
	if mode != "per_token" {
		return claim.CharityActual{}, claim.ErrInvariant
	}
	if input.UsageUnknown || !input.Usage.Present {
		var reserve int64
		if err := tx.QueryRowContext(ctx, `SELECT cr.token_reserve_milli
FROM charity_reservations cr WHERE cr.logical_request_id=?`, input.RequestID).Scan(&reserve); err != nil {
			return claim.CharityActual{}, fmt.Errorf("charity: read conservative token charge: %w", err)
		}
		return claim.CharityActual{PriceMilli: reserve, RewardMilli: 0}, nil
	}
	usage := credits.TokenUsage{
		UncachedInput: input.Usage.UncachedInputTokens, CacheWriteInput: input.Usage.CacheWriteInputTokens,
		CacheReadInput: input.Usage.CacheReadInputTokens, Output: input.Usage.OutputTokens,
	}
	price, err := credits.PriceTokenUsage(usage, credits.TokenPrices{
		UncachedInput: uncachedUser, CacheWriteInput: writeUser, CacheReadInput: readUser, Output: outputUser,
	})
	if err != nil || !validMoney(price) {
		return claim.CharityActual{}, claim.ErrInvariant
	}
	reward := int64(0)
	if !input.SuppressReward {
		reward, err = credits.PriceTokenUsage(usage, credits.TokenPrices{
			UncachedInput: uncachedReward, CacheWriteInput: writeReward, CacheReadInput: readReward, Output: outputReward,
		})
		if err != nil || !validMoney(reward) {
			return claim.CharityActual{}, claim.ErrInvariant
		}
	}
	return claim.CharityActual{PriceMilli: price, RewardMilli: reward}, nil
}

func (s *Service) CompleteAttempt(ctx context.Context, tx *sql.Tx, completion claim.CharityAttemptCompletion) error {
	input := completion.Attempt
	if s == nil || ctx == nil || tx == nil || !validAttemptInput(input) ||
		!validMoney(completion.Actual.PriceMilli) || !validMoney(completion.Actual.RewardMilli) {
		return claim.ErrInvalidInput
	}
	if !validRewardState(completion.RewardState, input, completion.Actual) {
		return claim.ErrConflict
	}
	want, err := prepareAttempt(ctx, tx, input)
	if err != nil {
		// A direct idempotent replay observes the already committed row below.
		if !errors.Is(err, claim.ErrConflict) {
			return err
		}
		return verifyCompletedAttempt(ctx, tx, completion)
	}
	if want != completion.Actual {
		return claim.ErrConflict
	}

	row, err := readUsageReservation(ctx, tx, input.RequestID, input.ClaimID)
	if err != nil {
		return err
	}
	if row.state != "reserved" || row.keyID == nil {
		return claim.ErrConflict
	}
	tokensActual, err := actualTokenCount(input, row.tokensReserved)
	if err != nil {
		return err
	}
	callsActual := 0
	if input.ResponseStarted {
		callsActual = 1
	}

	if err := settleKeyCapacity(ctx, tx, *row.keyID, row, completion.Actual, callsActual, tokensActual, input.CompletedAt); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_usage_reservations SET
price_actual_milli=?,reward_actual_milli=?,calls_actual=?,tokens_actual=?,protocol_success=?,usage_unknown=?,
state='committed',finalized_at=? WHERE claim_id=? AND state='reserved'`,
		completion.Actual.PriceMilli, completion.Actual.RewardMilli, callsActual, tokensActual,
		boolInt(input.ProtocolSuccess), boolInt(input.UsageUnknown), input.CompletedAt, input.ClaimID)
	if err != nil {
		return fmt.Errorf("charity: commit attempt usage: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if err := updateRequestAttemptAggregate(ctx, tx, input, completion.Actual, completion.RewardState, input.CompletedAt); err != nil {
		return err
	}
	return foldStreak(ctx, tx, *row.keyID, row.generation, input.CompletedAt)
}

func verifyCompletedAttempt(ctx context.Context, tx *sql.Tx, completion claim.CharityAttemptCompletion) error {
	var state string
	var price, reward, calls, tokens sql.NullInt64
	var success, unknown sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT state,price_actual_milli,reward_actual_milli,calls_actual,tokens_actual,
protocol_success,usage_unknown
FROM donation_usage_reservations WHERE claim_id=?`, completion.Attempt.ClaimID).Scan(
		&state, &price, &reward, &calls, &tokens, &success, &unknown)
	if errors.Is(err, sql.ErrNoRows) {
		return claim.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("charity: verify attempt replay: %w", err)
	}
	tokenCount, countErr := actualTokenCount(completion.Attempt, 0)
	if completion.Attempt.UsageUnknown {
		var reserved int64
		if err := tx.QueryRowContext(ctx, `SELECT tokens_reserved FROM donation_usage_reservations WHERE claim_id=?`,
			completion.Attempt.ClaimID).Scan(&reserved); err != nil {
			return fmt.Errorf("charity: verify conservative token replay: %w", err)
		}
		tokenCount, countErr = actualTokenCount(completion.Attempt, reserved)
	}
	callCount := int64(0)
	if completion.Attempt.ResponseStarted {
		callCount = 1
	}
	if countErr == nil && state == "committed" && price.Valid && reward.Valid && calls.Valid && tokens.Valid && success.Valid && unknown.Valid &&
		price.Int64 == completion.Actual.PriceMilli && reward.Int64 == completion.Actual.RewardMilli &&
		calls.Int64 == callCount && tokens.Int64 == tokenCount &&
		success.Int64 == int64(boolInt(completion.Attempt.ProtocolSuccess)) &&
		unknown.Int64 == int64(boolInt(completion.Attempt.UsageUnknown)) {
		return nil
	}
	return claim.ErrConflict
}

func actualTokenCount(input claim.CharityAttemptInput, reserve int64) (int64, error) {
	if !input.ResponseStarted {
		return 0, nil
	}
	if input.UsageUnknown || !input.Usage.Present {
		return reserve, nil
	}
	values := []int64{input.Usage.UncachedInputTokens, input.Usage.CacheWriteInputTokens,
		input.Usage.CacheReadInputTokens, input.Usage.OutputTokens}
	total := int64(0)
	for _, value := range values {
		if value < 0 || value > math.MaxInt32-total {
			return 0, claim.ErrInvariant
		}
		total += value
	}
	return total, nil
}

func settleKeyCapacity(
	ctx context.Context,
	tx *sql.Tx,
	keyID int64,
	reservation usageReservation,
	actual claim.CharityActual,
	callsActual int,
	tokensActual int64,
	at int64,
) error {
	var priceUsedBlob, priceReservedBlob, callsUsedBlob, callsReservedBlob, tokensUsedBlob, tokensReservedBlob []byte
	if err := tx.QueryRowContext(ctx, `SELECT price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved
FROM donation_keys WHERE id=?`, keyID).Scan(&priceUsedBlob, &priceReservedBlob, &callsUsedBlob,
		&callsReservedBlob, &tokensUsedBlob, &tokensReservedBlob); err != nil {
		return fmt.Errorf("charity: read settlement capacity: %w", err)
	}
	priceUsed, err := decodeU128(priceUsedBlob)
	if err != nil {
		return err
	}
	priceReserved, err := decodeU128(priceReservedBlob)
	if err != nil {
		return err
	}
	callsUsed, err := decodeU128(callsUsedBlob)
	if err != nil {
		return err
	}
	callsReserved, err := decodeU128(callsReservedBlob)
	if err != nil {
		return err
	}
	tokensUsed, err := decodeU128(tokensUsedBlob)
	if err != nil {
		return err
	}
	tokensReserved, err := decodeU128(tokensReservedBlob)
	if err != nil {
		return err
	}
	priceReserved, err = subU128(priceReserved, big.NewInt(reservation.priceReserved))
	if err != nil {
		return err
	}
	callsReserved, err = subU128(callsReserved, big.NewInt(int64(reservation.callsReserved)))
	if err != nil {
		return err
	}
	tokensReserved, err = subU128(tokensReserved, big.NewInt(reservation.tokensReserved))
	if err != nil {
		return err
	}
	priceUsed, err = addU128(priceUsed, big.NewInt(actual.PriceMilli))
	if err != nil {
		return err
	}
	callsUsed, err = addU128(callsUsed, big.NewInt(int64(callsActual)))
	if err != nil {
		return err
	}
	tokensUsed, err = addU128(tokensUsed, big.NewInt(tokensActual))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET
price_used_mag=?,price_reserved_mag=?,calls_used=?,calls_reserved=?,tokens_used=?,tokens_reserved=?,updated_at=?
WHERE id=? AND price_used_mag=? AND price_reserved_mag=? AND calls_used=? AND calls_reserved=? AND tokens_used=? AND tokens_reserved=?`,
		db.EncodeU128(priceUsed), db.EncodeU128(priceReserved), db.EncodeU128(callsUsed), db.EncodeU128(callsReserved),
		db.EncodeU128(tokensUsed), db.EncodeU128(tokensReserved), at, keyID,
		priceUsedBlob, priceReservedBlob, callsUsedBlob, callsReservedBlob, tokensUsedBlob, tokensReservedBlob)
	if err != nil {
		return fmt.Errorf("charity: settle key capacity: %w", err)
	}
	return requireOne(result)
}

func updateRequestAttemptAggregate(ctx context.Context, tx *sql.Tx, input claim.CharityAttemptInput, actual claim.CharityActual, rewardState claim.RewardState, at int64) error {
	var totalBlob []byte
	var u0, u1, u2, u3 int64
	var unknown int
	if err := tx.QueryRowContext(ctx, `SELECT donor_reward_total_mag,usage_uncached_input_tokens,
cache_write_input_tokens,cache_read_input_tokens,usage_output_tokens,usage_unknown
FROM charity_reservations WHERE logical_request_id=?`, input.RequestID).Scan(&totalBlob, &u0, &u1, &u2, &u3, &unknown); err != nil {
		return fmt.Errorf("charity: read request usage aggregate: %w", err)
	}
	total, err := decodeU128(totalBlob)
	if err != nil {
		return err
	}
	postedReward := int64(0)
	if rewardState == claim.RewardPosted {
		postedReward = actual.RewardMilli
	}
	total, err = addU128(total, big.NewInt(postedReward))
	if err != nil {
		return err
	}
	values := []int64{input.Usage.UncachedInputTokens, input.Usage.CacheWriteInputTokens,
		input.Usage.CacheReadInputTokens, input.Usage.OutputTokens}
	stored := []*int64{&u0, &u1, &u2, &u3}
	if input.ResponseStarted && input.Usage.Present {
		for index, value := range values {
			if value < 0 || *stored[index] > math.MaxInt64-value {
				return claim.ErrInvariant
			}
			*stored[index] += value
		}
	}
	if input.ResponseStarted && input.UsageUnknown {
		unknown = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE charity_reservations SET
state='dispatched',dispatched_at=COALESCE(dispatched_at,(SELECT dispatched_at FROM dispatch_claims WHERE id=?)),
donor_reward_total_mag=?,usage_uncached_input_tokens=?,cache_write_input_tokens=?,
cache_read_input_tokens=?,usage_output_tokens=?,usage_unknown=?,updated_at=?
WHERE logical_request_id=? AND state IN ('reserved','dispatched') AND donor_reward_total_mag=?`,
		input.ClaimID, db.EncodeU128(total), u0, u1, u2, u3, unknown, at, input.RequestID, totalBlob)
	if err != nil {
		return fmt.Errorf("charity: aggregate request usage: %w", err)
	}
	return requireOne(result)
}

func foldStreak(ctx context.Context, tx *sql.Tx, keyID int64, generation db.U128, at int64) error {
	var currentGenerationBlob, nextFoldBlob, streakBlob []byte
	var failureDisabled int
	if err := tx.QueryRowContext(ctx, `SELECT streak_generation,next_fold_seq,failure_streak,failure_disabled
FROM donation_keys WHERE id=?`, keyID).Scan(&currentGenerationBlob, &nextFoldBlob, &streakBlob, &failureDisabled); err != nil {
		return fmt.Errorf("charity: read streak cursor: %w", err)
	}
	currentGeneration, err := decodeU128(currentGenerationBlob)
	if err != nil {
		return err
	}
	if currentGeneration != generation {
		return nil
	}
	next, err := decodeU128(nextFoldBlob)
	if err != nil {
		return err
	}
	streak, err := decodeU128(streakBlob)
	if err != nil {
		return err
	}
	disabledNow := false
	for {
		var state string
		var success sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT state,protocol_success FROM donation_usage_reservations
WHERE donation_key_id=? AND streak_generation=? AND claim_seq=?`,
			keyID, db.EncodeU128(generation), db.EncodeU128(next)).Scan(&state, &success)
		if errors.Is(err, sql.ErrNoRows) || state == "reserved" {
			break
		}
		if err != nil {
			return fmt.Errorf("charity: read streak result: %w", err)
		}
		if state == "committed" {
			if !success.Valid {
				return claim.ErrInvariant
			}
			if success.Int64 == 1 {
				streak = db.U128{}
			} else {
				streak, err = addU128(streak, big.NewInt(1))
				if err != nil {
					return err
				}
				if failureDisabled == 0 && streak.Big().Cmp(big.NewInt(failureDisableCount)) >= 0 {
					failureDisabled = 1
					disabledNow = true
				}
			}
		} else if state != "released" {
			return claim.ErrInvariant
		}
		next, err = addU128(next, big.NewInt(1))
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET next_fold_seq=?,failure_streak=?,failure_disabled=?,updated_at=?
WHERE id=? AND streak_generation=? AND next_fold_seq=? AND failure_streak=?`,
		db.EncodeU128(next), db.EncodeU128(streak), failureDisabled, at, keyID,
		currentGenerationBlob, nextFoldBlob, streakBlob)
	if err != nil {
		return fmt.Errorf("charity: advance streak cursor: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if disabledNow {
		ref := fmt.Sprintf("donation-key:%d:generation:%s", keyID, generation.Decimal())
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
SELECT 'donation_failure_disabled','charity donation key disabled after consecutive protocol failures',?,?,0
WHERE NOT EXISTS(SELECT 1 FROM admin_alerts WHERE kind='donation_failure_disabled' AND ref=?)`, ref, at, ref); err != nil {
			return fmt.Errorf("charity: create failure alert: %w", err)
		}
	}
	return nil
}

func (s *Service) CompleteRequest(ctx context.Context, tx *sql.Tx, input claim.CharityRequestCompletion) error {
	if s == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(input.RequestID, "req_") || !validTime(input.CompletedAt) ||
		(input.Disposition != claim.AccountingCommit && input.Disposition != claim.AccountingRelease) {
		return claim.ErrInvalidInput
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM charity_reservations WHERE logical_request_id=?`, input.RequestID).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return claim.ErrNotFound
		}
		return fmt.Errorf("charity: read request terminal state: %w", err)
	}
	wantState := "committed"
	if input.Disposition == claim.AccountingRelease {
		wantState = "released"
	}
	if state == wantState {
		return nil
	}
	if state != "reserved" && state != "dispatched" {
		return claim.ErrConflict
	}
	var nonterminal int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM donation_usage_reservations u
JOIN dispatch_claims c ON c.id=u.claim_id
WHERE c.logical_request_id=? AND u.state='reserved'`, input.RequestID).Scan(&nonterminal); err != nil {
		return fmt.Errorf("charity: inspect terminal attempts: %w", err)
	}
	if nonterminal != 0 {
		return claim.ErrConflict
	}
	if input.Disposition == claim.AccountingRelease && state != "reserved" {
		return claim.ErrConflict
	}

	original, userCharge, err := calculateRequestChargeTx(ctx, tx, input.RequestID, input.Disposition)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE charity_reservations SET state=?,original_charge_milli=?,
user_charge_milli=?,finalized_at=?,updated_at=? WHERE logical_request_id=? AND state=?`,
		wantState, original, userCharge, input.CompletedAt, input.CompletedAt, input.RequestID, state)
	if err != nil {
		return fmt.Errorf("charity: finalize request reservation: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	if input.Disposition == claim.AccountingCommit {
		return recordModelOutcome(ctx, tx, input.RequestID, input.Caller.Class == claim.ResultSuccess, input.CompletedAt)
	}
	return nil
}

// RequestCharge computes the caller settlement inside the claim transaction.
func (s *Service) RequestCharge(ctx context.Context, tx *sql.Tx, requestID string, disposition claim.AccountingDisposition) (int64, error) {
	if s == nil || ctx == nil || tx == nil || !db.ValidateOpaqueID(requestID, "req_") ||
		(disposition != claim.AccountingCommit && disposition != claim.AccountingRelease) {
		return 0, claim.ErrInvalidInput
	}
	_, charge, err := calculateRequestChargeTx(ctx, tx, requestID, disposition)
	return charge, err
}

// CalculateRequestCharge previews the caller settlement after all attempts
// are terminal. Completion recomputes it in the transaction that posts it.
func (s *Service) CalculateRequestCharge(ctx context.Context, requestID string, disposition claim.AccountingDisposition) (int64, error) {
	if s == nil || s.db == nil || ctx == nil || !db.ValidateOpaqueID(requestID, "req_") ||
		(disposition != claim.AccountingCommit && disposition != claim.AccountingRelease) {
		return 0, claim.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("charity: begin charge read: %w", err)
	}
	defer tx.Rollback()
	_, charge, err := calculateRequestChargeTx(ctx, tx, requestID, disposition)
	if err != nil {
		return 0, err
	}
	return charge, nil
}

func calculateRequestChargeTx(ctx context.Context, tx *sql.Tx, requestID string, disposition claim.AccountingDisposition) (int64, int64, error) {
	if disposition == claim.AccountingRelease {
		return 0, 0, nil
	}
	var started bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_usage_reservations u
JOIN dispatch_claims c ON c.id=u.claim_id
WHERE c.logical_request_id=? AND u.state='committed' AND u.calls_actual=1)`, requestID).Scan(&started); err != nil {
		return 0, 0, fmt.Errorf("charity: read successful response evidence: %w", err)
	}
	if !started {
		return 0, 0, nil
	}
	var mode string
	var discount int
	var requestPrice, reserved int64
	var p0, p1, p2, p3, u0, u1, u2, u3 int64
	var unknown int
	err := tx.QueryRowContext(ctx, `SELECT pricing_mode,discount_percent,request_user_price_milli,user_reserved_milli,
uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,
usage_uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,usage_output_tokens,usage_unknown
FROM charity_reservations WHERE logical_request_id=?`, requestID).Scan(
		&mode, &discount, &requestPrice, &reserved, &p0, &p1, &p2, &p3, &u0, &u1, &u2, &u3, &unknown)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, claim.ErrNotFound
	}
	if err != nil {
		return 0, 0, fmt.Errorf("charity: calculate request charge: %w", err)
	}
	original := requestPrice
	if mode == "per_token" {
		original, err = credits.PriceTokenUsage(credits.TokenUsage{
			UncachedInput: u0, CacheWriteInput: u1, CacheReadInput: u2, Output: u3,
		}, credits.TokenPrices{UncachedInput: p0, CacheWriteInput: p1, CacheReadInput: p2, Output: p3})
		if err != nil {
			return 0, 0, claim.ErrInvariant
		}
		if unknown == 1 && original < reserved {
			original = reserved
		}
	} else if mode != "per_request" {
		return 0, 0, claim.ErrInvariant
	}
	charge, err := credits.ApplyDiscountPercent(original, discount)
	if err != nil {
		return 0, 0, claim.ErrInvariant
	}
	if charge > reserved {
		charge = reserved
	}
	return original, charge, nil
}

func recordModelOutcome(ctx context.Context, tx *sql.Tx, requestID string, success bool, at int64) error {
	var modelID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT charity_model_id FROM charity_reservations WHERE logical_request_id=?`, requestID).Scan(&modelID); err != nil {
		return fmt.Errorf("charity: read outcome model: %w", err)
	}
	if !modelID.Valid {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_stats(model_id) VALUES(?)
ON CONFLICT(model_id) DO NOTHING`, modelID.Int64); err != nil {
		return fmt.Errorf("charity: initialize model stats: %w", err)
	}
	var slot, samples, successes int
	if err := tx.QueryRowContext(ctx, `SELECT next_slot,sample_count,success_count FROM charity_model_stats WHERE model_id=?`, modelID.Int64).Scan(&slot, &samples, &successes); err != nil {
		return fmt.Errorf("charity: read model stats: %w", err)
	}
	var old sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT success FROM charity_model_outcomes WHERE model_id=? AND slot=?`, modelID.Int64, slot).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("charity: read rolling outcome: %w", err)
	}
	value := boolInt(success)
	if old.Valid {
		successes -= int(old.Int64)
	} else {
		samples++
	}
	successes += value
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_model_outcomes(model_id,slot,success,created_at)
VALUES(?,?,?,?) ON CONFLICT(model_id,slot) DO UPDATE SET success=excluded.success,created_at=excluded.created_at`,
		modelID.Int64, slot, value, at); err != nil {
		return fmt.Errorf("charity: persist rolling outcome: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE charity_model_stats SET next_slot=?,sample_count=?,success_count=?
WHERE model_id=? AND next_slot=? AND sample_count=? AND success_count=?`,
		(slot+1)%100, samples, successes, modelID.Int64, slot, func() int {
			if old.Valid {
				return samples
			}
			return samples - 1
		}(), successes-value+func() int {
			if old.Valid {
				return int(old.Int64)
			}
			return 0
		}())
	if err != nil {
		return fmt.Errorf("charity: advance model stats: %w", err)
	}
	return requireOne(result)
}

// Cleanup removes only terminal charity reservations after the fixed retention
// boundary. In-flight rows are intentionally never age-deleted.
func (s *Service) Cleanup(ctx context.Context, decisionNow int64, limit int) (int, error) {
	if s == nil || s.db == nil || ctx == nil || !validTime(decisionNow) || limit < 1 || limit > 100 {
		return 0, claim.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("charity: begin cleanup: %w", err)
	}
	defer tx.Rollback()
	count, err := s.CleanupTx(ctx, tx, decisionNow, limit)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("charity: commit cleanup: %w", err)
	}
	return count, nil
}

func validAttemptInput(input claim.CharityAttemptInput) bool {
	if !db.ValidateOpaqueID(input.RequestID, "req_") || !db.ValidateOpaqueID(input.ClaimID, "clm_") ||
		!validTime(input.CompletedAt) || input.UsageUnknown != !input.Usage.Present {
		return false
	}
	if input.ProtocolSuccess && !input.ResponseStarted {
		return false
	}
	if input.DonationKeyID != nil && *input.DonationKeyID <= 0 || input.ReceiverUserID != nil && *input.ReceiverUserID <= 0 {
		return false
	}
	if input.Usage.UncachedInputTokens < 0 || input.Usage.CacheWriteInputTokens < 0 ||
		input.Usage.CacheReadInputTokens < 0 || input.Usage.OutputTokens < 0 {
		return false
	}
	return true
}

func validRewardState(state claim.RewardState, input claim.CharityAttemptInput, actual claim.CharityActual) bool {
	switch state {
	case claim.RewardPosted:
		return actual.RewardMilli > 0 && input.ReceiverUserID != nil && !input.SuppressReward
	case claim.RewardZero:
		return actual.RewardMilli == 0
	case claim.RewardReceiverDeleted:
		return actual.RewardMilli > 0 && input.ReceiverUserID == nil && !input.SuppressReward
	default:
		return false
	}
}

func decodeU128(blob []byte) (db.U128, error) {
	value, err := db.DecodeU128(blob)
	if err != nil {
		return db.U128{}, claim.ErrInvariant
	}
	return value, nil
}

func addU128(value db.U128, increment *big.Int) (db.U128, error) {
	if increment == nil || increment.Sign() < 0 {
		return db.U128{}, claim.ErrInvariant
	}
	result, err := db.U128FromBig(new(big.Int).Add(value.Big(), increment))
	if err != nil {
		return db.U128{}, claim.ErrInvariant
	}
	return result, nil
}

func subU128(value db.U128, decrement *big.Int) (db.U128, error) {
	if decrement == nil || decrement.Sign() < 0 || value.Big().Cmp(decrement) < 0 {
		return db.U128{}, claim.ErrInvariant
	}
	result, err := db.U128FromBig(new(big.Int).Sub(value.Big(), decrement))
	if err != nil {
		return db.U128{}, claim.ErrInvariant
	}
	return result, nil
}

func requireOne(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("charity: inspect state transition: %w", err)
	}
	if count != 1 {
		return claim.ErrConflict
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validMoney(value int64) bool { return value >= 0 && value <= claim.MaxMoneyMilli }
func validTime(value int64) bool  { return value >= 0 && value <= maxUnixSecond }

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	// All production dependencies are pointer-backed. This small type switch
	// catches the typed-nil cases used by composition tests without reflection.
	switch v := value.(type) {
	case *Service:
		return v == nil
	default:
		return false
	}
}

// Keep the connector-neutral usage type reachable from this package's public
// surface for routing/forward composition without exposing vendor payloads.
type Usage = connectorcontract.Usage
