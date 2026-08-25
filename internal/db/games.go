package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

const (
	GameAutoSettleDelay   = 120 * time.Second
	GameRecoveryBatchMax  = 100
	GameRetryInitialDelay = 30 * time.Second
	GameRetryMaximumDelay = time.Hour
	maxGameAttemptCount   = int64(2147483647)
)

var (
	ErrGameDisabled        = errors.New("db: game is disabled")
	ErrGamePending         = errors.New("db: unfinished game round exists")
	ErrGameRateLimited     = errors.New("db: game start rate limited")
	ErrGameLedgerCollision = errors.New("db: game ledger operation collision")
	ErrGameInvalid         = errors.New("db: invalid game request")
	ErrGameInternal        = errors.New("db: game state is inconsistent")
)

// GameRateLimitError carries only the safe retry duration from the dedicated
// start limiter. It never contains an idempotency key or user identifier.
type GameRateLimitError struct {
	RetryAfter time.Duration
	Capacity   bool
}

func (e *GameRateLimitError) Error() string { return ErrGameRateLimited.Error() }
func (e *GameRateLimitError) Unwrap() error { return ErrGameRateLimited }

type GameSettlementState string

const (
	GameReserved  GameSettlementState = "reserved"
	GameCommitted GameSettlementState = "committed"
	GameReleased  GameSettlementState = "released"
)

// FishingRound is the narrow authoritative projection shared with the later
// HTTP/recovery layer. Outcome is nil for reserved and released rounds.
type FishingRound struct {
	RoundID       string
	SettlementID  string
	State         GameSettlementState
	Bait          fishing.Bait
	EntryMilli    int64
	PayoutMilli   int64
	BalanceMilli  int64
	CreatedAt     time.Time
	AutoSettleAt  time.Time
	SettledAt     *time.Time
	Outcome       *fishing.Outcome
	Replayed      bool
	EventSequence int64
}

type StartFishingInput struct {
	UserID         int64
	Bait           fishing.Bait
	IdempotencyKey string
}

// GameSettlementService owns the in-process start limiter and all game
// economic transactions. It has no goroutine; the caller owns due scheduling.
type GameSettlementService struct {
	store         *Store
	starts        *game.StartLimiter
	now           func() time.Time
	idReader      io.Reader
	outcomeSource fishing.IntSource
	// commitTx is instance-local so same-package fault tests can fail the
	// commit boundary deterministically. The constructor always installs the
	// real transaction Commit method; it is not runtime configuration.
	commitTx func(*sql.Tx) error
	randomMu sync.Mutex
}

type GameSettlementServiceConfig struct {
	Store         *Store
	Now           func() time.Time
	IDReader      io.Reader
	OutcomeSource fishing.IntSource
	MaxStartUsers int
}

func NewGameSettlementService(config GameSettlementServiceConfig) (*GameSettlementService, error) {
	if config.Store == nil {
		return nil, errors.New("db: game store is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.IDReader == nil {
		config.IDReader = rand.Reader
	}
	if config.OutcomeSource == nil {
		config.OutcomeSource = fishing.CryptoSource{}
	}
	starts, err := game.NewStartLimiter(game.StartLimiterConfig{
		Now: config.Now, MaxUsers: config.MaxStartUsers,
	})
	if err != nil {
		return nil, err
	}
	return &GameSettlementService{
		store: config.Store, starts: starts, now: config.Now,
		idReader: config.IDReader, outcomeSource: config.OutcomeSource,
		commitTx: func(tx *sql.Tx) error { return tx.Commit() },
	}, nil
}

func (s *GameSettlementService) Close() error {
	if s == nil || s.starts == nil {
		return nil
	}
	return s.starts.Close()
}

// BeginUserDeletion blocks new game starts until the caller commits or aborts
// its authoritative account-delete transaction.
func (s *GameSettlementService) BeginUserDeletion(userID int64) (commit func() bool, abort func() bool, err error) {
	if s == nil || s.starts == nil {
		return nil, nil, game.ErrStartClosed
	}
	return s.starts.BeginUserDeletion(userID)
}

func validateStartFishingInput(input StartFishingInput) error {
	if input.UserID <= 0 || !validIdempotencyKey(input.IdempotencyKey) {
		return ErrGameInvalid
	}
	if _, err := game.Resolve(game.FishingID, game.FishingVersion); err != nil {
		return ErrGameInternal
	}
	if !validFishingBait(input.Bait) {
		return ErrGameInvalid
	}
	return nil
}

func validIdempotencyKey(value string) bool {
	if value == "" || len(value) > 128 || strings.HasPrefix(value, SystemOperationPrefix) {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character == '-' || character == '_' || character == '.' || character == ':' || character == '@':
		default:
			return false
		}
	}
	return true
}

// ValidGameIdempotencyKey exposes the exact start-key grammar to the HTTP
// boundary without duplicating it in another package. The raw token is never
// persisted; StartFishingRound stores only its digest.
func ValidGameIdempotencyKey(value string) bool { return validIdempotencyKey(value) }

func fishingStartHashes(input StartFishingInput) ([sha256.Size]byte, [sha256.Size]byte) {
	keyHash := sha256.Sum256([]byte(input.IdempotencyKey))
	requestHash := sha256.Sum256([]byte("fishing\x00v1\x00" + string(input.Bait)))
	return keyHash, requestHash
}

// StartFishingRound resolves exact replay before tentative admission, then
// repeats the replay check and every economic decision inside one write
// transaction. Only a committed database result is returned.
func (s *GameSettlementService) StartFishingRound(ctx context.Context, input StartFishingInput) (FishingRound, error) {
	if s == nil || s.store == nil || ctx == nil {
		return FishingRound{}, ErrGameInternal
	}
	if err := validateStartFishingInput(input); err != nil {
		return FishingRound{}, err
	}
	keyHash, requestHash := fishingStartHashes(input)
	replay, found, err := s.findFishingReplay(ctx, input.UserID, keyHash, requestHash)
	if err != nil || found {
		return replay, err
	}
	reservation, retryAfter, err := s.starts.Reserve(input.UserID)
	if err != nil {
		switch {
		case errors.Is(err, game.ErrStartRateLimited):
			return FishingRound{}, &GameRateLimitError{RetryAfter: retryAfter}
		case errors.Is(err, game.ErrStartCapacity):
			return FishingRound{}, &GameRateLimitError{Capacity: true}
		case errors.Is(err, game.ErrUserDeleting):
			return FishingRound{}, ErrNotFound
		default:
			return FishingRound{}, ErrGameInternal
		}
	}
	terminal := false
	defer func() {
		if !terminal {
			reservation.Release()
		}
	}()
	result, consumeLimit, err := s.startFishingTx(ctx, input, keyHash, requestHash)
	if consumeLimit {
		reservation.Commit()
	} else {
		reservation.Release()
	}
	terminal = true
	return result, err
}

func (s *GameSettlementService) findFishingReplay(ctx context.Context, userID int64, keyHash, requestHash [sha256.Size]byte) (FishingRound, bool, error) {
	return queryFishingReplay(ctx, s.store.db, userID, keyHash, requestHash)
}

type queryRowContext interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func queryFishingReplay(ctx context.Context, query queryRowContext, userID int64, keyHash, requestHash [sha256.Size]byte) (FishingRound, bool, error) {
	var round FishingRound
	var storedRequest []byte
	var state, bait, species, tier string
	var payoutMilli int64
	var size int
	var createdAt, autoSettleAt int64
	var settledAt sql.NullInt64
	err := query.QueryRowContext(ctx, `
SELECT r.id, r.settlement_id, r.start_request_hash, r.event_seq, r.created_at, r.settled_at,
       s.state, s.entry_milli, s.payout_milli, s.auto_settle_at, u.credits,
       o.bait,o.species_key,o.tier,o.size_cm
FROM game_rounds r
JOIN game_settlements s ON s.id=r.settlement_id
JOIN users u ON u.id=r.user_id
JOIN game_fishing_outcomes o ON o.round_id=r.id
WHERE r.user_id=? AND r.game_type=? AND r.start_key_hash=?`,
		userID, game.FishingID, keyHash[:]).Scan(
		&round.RoundID, &round.SettlementID, &storedRequest, &round.EventSequence,
		&createdAt, &settledAt, &state, &round.EntryMilli, &payoutMilli, &autoSettleAt, &round.BalanceMilli,
		&bait, &species, &tier, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return FishingRound{}, false, nil
	}
	if err != nil {
		return FishingRound{}, false, fmt.Errorf("game replay lookup: %w", err)
	}
	if len(storedRequest) != sha256.Size || !equalDigest(storedRequest, requestHash) {
		return FishingRound{}, true, ErrConflict
	}
	if !validGameState(state) {
		return FishingRound{}, true, ErrGameInternal
	}
	round.State = GameSettlementState(state)
	round.Bait = fishing.Bait(bait)
	if !validFishingBait(round.Bait) {
		return FishingRound{}, true, ErrGameInternal
	}
	switch round.State {
	case GameReserved:
		if settledAt.Valid || round.EventSequence != 1 {
			return FishingRound{}, true, ErrGameInternal
		}
	case GameCommitted, GameReleased:
		if !settledAt.Valid || round.EventSequence < 2 {
			return FishingRound{}, true, ErrGameInternal
		}
	}
	if round.State == GameCommitted {
		round.PayoutMilli = payoutMilli
		round.Outcome = &fishing.Outcome{
			Bait: fishing.Bait(bait), Key: species, Tier: fishing.Tier(tier), SizeCentimetre: size,
		}
	}
	round.CreatedAt = time.Unix(createdAt, 0)
	round.AutoSettleAt = time.Unix(autoSettleAt, 0)
	if round.AutoSettleAt.Before(round.CreatedAt) {
		return FishingRound{}, true, ErrGameInternal
	}
	if settledAt.Valid {
		settled := time.Unix(settledAt.Int64, 0)
		round.SettledAt = &settled
	}
	round.Replayed = true
	return round, true, nil
}

func validGameState(state string) bool {
	return state == string(GameReserved) || state == string(GameCommitted) || state == string(GameReleased)
}

func validFishingBait(bait fishing.Bait) bool {
	return bait == fishing.BaitWorm || bait == fishing.BaitLure || bait == fishing.BaitPremium
}

func (s *GameSettlementService) startFishingTx(ctx context.Context, input StartFishingInput, keyHash, requestHash [sha256.Size]byte) (FishingRound, bool, error) {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return FishingRound{}, false, fmt.Errorf("game start begin: %w", err)
	}
	defer tx.Rollback()

	if replay, found, replayErr := queryFishingReplay(ctx, tx, input.UserID, keyHash, requestHash); replayErr != nil || found {
		return replay, false, replayErr
	}
	masterEnabled, fishingEnabled, err := readGameSwitchesTx(ctx, tx)
	if err != nil {
		return FishingRound{}, false, err
	}
	if !masterEnabled || !fishingEnabled {
		return FishingRound{}, false, ErrGameDisabled
	}
	nowUnix := s.now().Unix()
	now := time.Unix(nowUnix, 0)
	if _, err := liftDueUserBanTx(tx, input.UserID, nowUnix); err != nil {
		return FishingRound{}, false, fmt.Errorf("game start user gate: %w", err)
	}
	var isAdmin, isBanned int
	var creditsMilli int64
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,credits FROM users WHERE id=?`, input.UserID).
		Scan(&isAdmin, &isBanned, &creditsMilli); errors.Is(err, sql.ErrNoRows) {
		return FishingRound{}, false, ErrNotFound
	} else if err != nil {
		return FishingRound{}, false, fmt.Errorf("game start user gate: %w", err)
	}
	if isAdmin != 0 {
		return FishingRound{}, false, ErrAdminProtected
	}
	if isBanned != 0 {
		return FishingRound{}, false, ErrBanned
	}
	snapshot, err := readGameConfigTx(ctx, tx)
	if err != nil {
		return FishingRound{}, false, err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rounds WHERE user_id=? AND game_type=? AND settled_at IS NULL`, input.UserID, game.FishingID).Scan(&pending); err != nil {
		return FishingRound{}, false, fmt.Errorf("game start pending check: %w", err)
	}
	if pending != 0 {
		return FishingRound{}, true, ErrGamePending
	}
	entry, entryErr := snapshot.Rules.Evidence(input.Bait)
	if entryErr != nil {
		return FishingRound{}, false, ErrGameInternal
	}
	entryMilli, parseErr := credits.ParseAmount(entry.EntryMilli)
	if parseErr != nil {
		return FishingRound{}, false, ErrGameInternal
	}
	if creditsMilli < entryMilli {
		return FishingRound{}, true, ErrInsufficientCredits
	}

	s.randomMu.Lock()
	rolled, rollErr := snapshot.Rules.Roll(input.Bait, s.outcomeSource)
	var roundID, settlementID string
	if rollErr == nil {
		roundID, rollErr = generateGameID("grd_", s.idReader)
	}
	if rollErr == nil {
		settlementID, rollErr = generateGameID("gst_", s.idReader)
	}
	s.randomMu.Unlock()
	if rollErr != nil {
		return FishingRound{}, false, fmt.Errorf("game start randomness: %w", rollErr)
	}
	delaySeconds := int64(GameAutoSettleDelay / time.Second)
	if nowUnix > math.MaxInt64-delaySeconds {
		return FishingRound{}, false, ErrGameInternal
	}
	autoSettleAt := nowUnix + delaySeconds
	if _, err := tx.ExecContext(ctx, `
INSERT INTO game_settlements
 (id,user_id,game_type,game_version,state,entry_milli,payout_milli,auto_settle_at,next_attempt_at,created_at,updated_at)
VALUES (?,?,?,?,'reserved',?,?,?,?,?,?)`, settlementID, input.UserID, game.FishingID,
		game.FishingVersion, rolled.Settlement.EntryMilli, rolled.Settlement.PayoutMilli,
		autoSettleAt, autoSettleAt, nowUnix, nowUnix); err != nil {
		return FishingRound{}, false, fmt.Errorf("game start settlement insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO game_rounds
 (id,user_id,game_type,game_version,settlement_id,start_key_hash,start_request_hash,event_seq,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,1,?,?)`, roundID, input.UserID, game.FishingID, game.FishingVersion,
		settlementID, keyHash[:], requestHash[:], nowUnix, nowUnix); err != nil {
		if isConstraintError(err) {
			var count int
			if queryErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_rounds WHERE user_id=? AND game_type=? AND settled_at IS NULL`, input.UserID, game.FishingID).Scan(&count); queryErr == nil && count > 0 {
				return FishingRound{}, true, ErrGamePending
			}
		}
		return FishingRound{}, false, fmt.Errorf("game start round insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO game_fishing_outcomes(round_id,bait,species_key,tier,size_cm)
VALUES (?,?,?,?,?)`, roundID, string(rolled.Outcome.Bait), rolled.Outcome.Key,
		string(rolled.Outcome.Tier), rolled.Outcome.SizeCentimetre); err != nil {
		return FishingRound{}, false, fmt.Errorf("game start outcome insert: %w", err)
	}
	operation := CreditOperation{
		Kind: LedgerGameReserve, UserID: input.UserID,
		OperationID:  gameLedgerOperationID(settlementID, "reserve"),
		CreditsDelta: -rolled.Settlement.EntryMilli, GameSettlementID: settlementID,
		CreatedAt: now, hasCreditFloor: true, creditFloor: rolled.Settlement.EntryMilli,
	}
	if err := operation.validate(); err != nil {
		return FishingRound{}, false, ErrGameInternal
	}
	ledger, err := s.store.applyCreditOperationTx(ctx, tx, operation)
	if err != nil {
		return FishingRound{}, false, err
	}
	if !ledger.Applied {
		return FishingRound{}, false, ErrGameLedgerCollision
	}
	dayKey, err := siteDayKeyAtTx(tx, nowUnix)
	if err == nil {
		inserted, activityErr := recordActivityTx(ctx, tx, input.UserID, dayKey, ActivityDelta{GameRounds: 1}, nowUnix)
		if activityErr != nil || !inserted {
			if activityErr != nil {
				return FishingRound{}, false, fmt.Errorf("game start activity: %w", activityErr)
			}
			return FishingRound{}, false, ErrNotFound
		}
	} else if !errors.Is(err, ErrTimezoneUnavailable) {
		return FishingRound{}, false, fmt.Errorf("game start activity day: %w", err)
	}
	if err := s.commitTx(tx); err != nil {
		return FishingRound{}, false, fmt.Errorf("game start commit: %w", err)
	}
	return FishingRound{
		RoundID: roundID, SettlementID: settlementID, State: GameReserved,
		Bait: input.Bait, EntryMilli: rolled.Settlement.EntryMilli,
		BalanceMilli: ledger.CreditsAfter, CreatedAt: now,
		AutoSettleAt: time.Unix(autoSettleAt, 0), EventSequence: 1,
	}, true, nil
}

func readGameSwitchesTx(ctx context.Context, tx *sql.Tx) (bool, bool, error) {
	read := func(key string) (bool, error) {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("game switch read: %w", err)
		}
		// Only the canonical enabled byte turns a feature on. Every malformed
		// representation is an intentional fail-closed disabled projection.
		return raw == "1", nil
	}
	master, err := read(game.GamesEnabledKey)
	if err != nil {
		return false, false, err
	}
	fishingEnabled, err := read(game.FishingEnabledKey)
	if err != nil {
		return false, false, err
	}
	return master, fishingEnabled, nil
}

func readGameConfigTx(ctx context.Context, tx *sql.Tx) (game.ConfigSnapshot, error) {
	raw := make(map[string]string, len(game.SiteConfigKeys()))
	for _, key := range game.SiteConfigKeys() {
		var value string
		err := tx.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return game.ConfigSnapshot{}, fmt.Errorf("game config read: %w", err)
		}
		raw[key] = value
	}
	snapshot, err := game.CompileConfig(raw)
	if err != nil {
		return game.ConfigSnapshot{}, ErrInvalidSiteConfig
	}
	return snapshot, nil
}

func generateGameID(prefix string, reader io.Reader) (string, error) {
	var entropy [16]byte
	if _, err := io.ReadFull(reader, entropy[:]); err != nil {
		return "", err
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:])
	value := prefix + strings.ToLower(encoded)
	if !ValidGameSettlementID(value) {
		return "", ErrGameInternal
	}
	return value, nil
}

func gameLedgerOperationID(settlementID, action string) string {
	return "sys.game." + settlementID + "." + action
}

// RecoverPendingFishingRound returns only the pending summary. The persisted
// outcome remains hidden until a committed settlement projection is queried.
func (s *GameSettlementService) RecoverPendingFishingRound(ctx context.Context, userID int64) (FishingRound, error) {
	if s == nil || s.store == nil || ctx == nil || userID <= 0 {
		return FishingRound{}, ErrNotFound
	}
	var round FishingRound
	var createdAt, autoSettleAt int64
	err := s.store.db.QueryRowContext(ctx, `
SELECT r.id,r.settlement_id,r.event_seq,r.created_at,s.auto_settle_at,s.entry_milli,u.credits,o.bait
FROM game_rounds r
JOIN game_settlements s ON s.id=r.settlement_id AND s.state='reserved'
JOIN users u ON u.id=r.user_id
JOIN game_fishing_outcomes o ON o.round_id=r.id
WHERE r.user_id=? AND r.game_type=? AND r.settled_at IS NULL`, userID, game.FishingID).
		Scan(&round.RoundID, &round.SettlementID, &round.EventSequence, &createdAt, &autoSettleAt,
			&round.EntryMilli, &round.BalanceMilli, &round.Bait)
	if errors.Is(err, sql.ErrNoRows) {
		return FishingRound{}, ErrNotFound
	}
	if err != nil {
		return FishingRound{}, fmt.Errorf("recover pending game: %w", err)
	}
	round.State = GameReserved
	round.CreatedAt = time.Unix(createdAt, 0)
	round.AutoSettleAt = time.Unix(autoSettleAt, 0)
	if round.EventSequence != 1 || !validFishingBait(round.Bait) || round.AutoSettleAt.Before(round.CreatedAt) {
		return FishingRound{}, ErrGameInternal
	}
	return round, nil
}
