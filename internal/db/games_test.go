package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
)

type gameTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *gameTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *gameTestClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type countingIDReader struct {
	mu   sync.Mutex
	next uint64
}

func (r *countingIDReader) Read(target []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	clear(target)
	value := r.next
	for index := len(target) - 1; index >= 0 && value != 0; index-- {
		target[index] = byte(value)
		value >>= 8
	}
	return len(target), nil
}

type zeroIntSource struct{}

func (zeroIntSource) Uint64n(uint64) (uint64, error) { return 0, nil }

type failingIntSource struct{}

func (failingIntSource) Uint64n(uint64) (uint64, error) { return 0, io.ErrUnexpectedEOF }

type cancelingIntSource struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (s *cancelingIntSource) Uint64n(uint64) (uint64, error) {
	s.once.Do(s.cancel)
	return 0, nil
}

type scriptedIntSource struct {
	mu     sync.Mutex
	values []uint64
}

func (s *scriptedIntSource) Uint64n(upper uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.values) == 0 {
		return 0, errors.New("script exhausted")
	}
	value := s.values[0]
	s.values = s.values[1:]
	if value >= upper {
		return 0, fmt.Errorf("script value %d outside %d", value, upper)
	}
	return value, nil
}

type gameTestRig struct {
	store   *Store
	service *GameSettlementService
	user    *User
	clock   *gameTestClock
	path    string
}

func newGameTestRig(t *testing.T, source fishing.IntSource, credits, donation int64, activity bool) *gameTestRig {
	t.Helper()
	path := filepath.Join(privateDBDir(t), "game.db")
	store := openTestStore(t, path)
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.CreateDiscordUser("game-user", "game-user", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE users SET credits=?,donation_credit=? WHERE id=?`, credits, donation, user.ID); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		game.GamesEnabledKey:   "1",
		game.FishingEnabledKey: "1",
	} {
		if _, err := store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if activity {
		if _, err := store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SiteTimezoneKey, "0"); err != nil {
			t.Fatal(err)
		}
	}
	clock := &gameTestClock{now: time.Unix(1_800_000_000, 0)}
	service, err := NewGameSettlementService(GameSettlementServiceConfig{
		Store: store, Now: clock.Now, IDReader: &countingIDReader{}, OutcomeSource: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return &gameTestRig{store: store, service: service, user: user, clock: clock, path: path}
}

func gameCount(t *testing.T, store *Store, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := store.DB().QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestGameStartTokenHashAndIDContract(t *testing.T) {
	for _, value := range []string{"a", "A0-_.:@z", strings.Repeat("x", 128)} {
		if !validIdempotencyKey(value) {
			t.Fatalf("valid token rejected: length=%d value=%q", len(value), value)
		}
	}
	for _, value := range []string{"", strings.Repeat("x", 129), "sys.reserved", "bad/value", "bad value", "非ASCII"} {
		if validIdempotencyKey(value) {
			t.Fatalf("invalid token accepted: %q", value)
		}
	}
	input := StartFishingInput{UserID: 1, Bait: fishing.BaitWorm, IdempotencyKey: "opaque-token"}
	keyHash, requestHash := fishingStartHashes(input)
	if keyHash != sha256.Sum256([]byte("opaque-token")) || requestHash != sha256.Sum256([]byte("fishing\x00v1\x00worm")) {
		t.Fatal("start hashes do not match the frozen byte contract")
	}
	id, err := generateGameID("grd_", bytes.NewReader(make([]byte, 16)))
	if err != nil || len(id) != 30 || !strings.HasPrefix(id, "grd_") || !ValidGameSettlementID(id) {
		t.Fatalf("128-bit game id = %q, %v", id, err)
	}
}

func TestGameStartAtomicReplayActivityAndLedger(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 777, true)
	ctx := context.Background()
	input := StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "start-one"}
	round, err := trig.service.StartFishingRound(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if round.State != GameReserved || round.Bait != fishing.BaitWorm || round.Outcome != nil || round.EntryMilli != 2_500_000 || round.PayoutMilli != 0 || round.BalanceMilli != 7_500_000 || !round.AutoSettleAt.Equal(round.CreatedAt.Add(GameAutoSettleDelay)) || round.EventSequence != 1 {
		t.Fatalf("start = %#v", round)
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements`) != 1 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 1 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_outcomes`) != 1 {
		t.Fatal("start did not persist exactly one complete round")
	}
	var kind, correlation string
	var creditsDelta, donationDelta int64
	if err := trig.store.DB().QueryRow(`SELECT kind,game_settlement_id,credits_delta,donation_credit_delta
FROM credit_ledger WHERE operation_id=?`, gameLedgerOperationID(round.SettlementID, "reserve")).
		Scan(&kind, &correlation, &creditsDelta, &donationDelta); err != nil {
		t.Fatal(err)
	}
	if kind != string(LedgerGameReserve) || correlation != round.SettlementID || creditsDelta != -2_500_000 || donationDelta != 0 {
		t.Fatalf("reserve ledger = %q %q %d %d", kind, correlation, creditsDelta, donationDelta)
	}
	var creditsAfter, donationAfter int64
	if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&creditsAfter, &donationAfter); err != nil {
		t.Fatal(err)
	}
	if creditsAfter != 7_500_000 || donationAfter != 777 {
		t.Fatalf("balances = %d/%d", creditsAfter, donationAfter)
	}
	var product, gameActive, rounds int64
	if err := trig.store.DB().QueryRow(`SELECT product_active,game_active,game_rounds FROM user_activity_daily WHERE user_id=?`, trig.user.ID).
		Scan(&product, &gameActive, &rounds); err != nil {
		t.Fatal(err)
	}
	if product != 1 || gameActive != 1 || rounds != 1 {
		t.Fatalf("activity = %d/%d/%d", product, gameActive, rounds)
	}

	replay, err := trig.service.StartFishingRound(ctx, input)
	if err != nil || !replay.Replayed || replay.RoundID != round.RoundID || replay.Bait != fishing.BaitWorm || replay.BalanceMilli != creditsAfter {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 1 ||
		gameCount(t, trig.store, `SELECT game_rounds FROM user_activity_daily WHERE user_id=?`, trig.user.ID) != 1 {
		t.Fatal("replay wrote ledger or activity")
	}
	input.Bait = fishing.BaitLure
	if _, err := trig.service.StartFishingRound(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key different bait = %v", err)
	}
}

func TestGameReservedProjectionNeverLeaksPersistedPayout(t *testing.T) {
	// First positive interval is a small fish, so the persisted payout is
	// non-zero and a zero service projection proves it was intentionally hidden.
	source := &scriptedIntSource{values: []uint64{968, 0, 10}}
	trig := newGameTestRig(t, source, 10_000_000, 0, false)
	input := StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "hidden"}
	started, err := trig.service.StartFishingRound(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var persistedPayout int64
	if err := trig.store.DB().QueryRow(`SELECT payout_milli FROM game_settlements WHERE id=?`, started.SettlementID).Scan(&persistedPayout); err != nil {
		t.Fatal(err)
	}
	if persistedPayout <= 0 || started.Bait != fishing.BaitWorm || started.PayoutMilli != 0 || started.Outcome != nil {
		t.Fatalf("reserved start leaked result: persisted=%d projection=%#v", persistedPayout, started)
	}
	replay, err := trig.service.StartFishingRound(context.Background(), input)
	if err != nil || replay.Bait != fishing.BaitWorm || replay.PayoutMilli != 0 || replay.Outcome != nil || replay.AutoSettleAt.IsZero() {
		t.Fatalf("reserved replay leaked result: %#v, %v", replay, err)
	}
	recovered, err := trig.service.RecoverPendingFishingRound(context.Background(), trig.user.ID)
	if err != nil || recovered.Bait != fishing.BaitWorm || recovered.PayoutMilli != 0 || recovered.Outcome != nil || !recovered.AutoSettleAt.Equal(started.AutoSettleAt) {
		t.Fatalf("pending recovery leaked result: %#v, %v", recovered, err)
	}
	committed, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: started.RoundID})
	if err != nil || committed.Bait != fishing.BaitWorm || committed.PayoutMilli != persistedPayout || committed.Outcome == nil {
		t.Fatalf("committed projection omitted result: %#v, %v", committed, err)
	}
	terminalReplay, err := trig.service.StartFishingRound(context.Background(), input)
	if err != nil || terminalReplay.State != GameCommitted || terminalReplay.PayoutMilli != persistedPayout || terminalReplay.Outcome == nil {
		t.Fatalf("terminal replay projection = %#v, %v", terminalReplay, err)
	}
}

func TestGameTimestampsAreReplayStableAtDatabasePrecision(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
	trig.clock.mu.Lock()
	trig.clock.now = time.Unix(1_800_000_000, 987_654_321)
	trig.clock.mu.Unlock()
	input := StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "stable-time"}
	started, err := trig.service.StartFishingRound(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if started.CreatedAt.Nanosecond() != 0 || started.AutoSettleAt.Nanosecond() != 0 {
		t.Fatalf("start timestamp precision = %#v", started)
	}
	replayed, err := trig.service.StartFishingRound(context.Background(), input)
	if err != nil || !replayed.CreatedAt.Equal(started.CreatedAt) || !replayed.AutoSettleAt.Equal(started.AutoSettleAt) {
		t.Fatalf("start timestamp replay = %#v, %v", replayed, err)
	}
	trig.clock.Advance(1500 * time.Millisecond)
	settled, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: started.RoundID})
	if err != nil || settled.SettledAt == nil || settled.SettledAt.Nanosecond() != 0 {
		t.Fatalf("settled timestamp = %#v, %v", settled, err)
	}
	terminalReplay, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: started.RoundID})
	if err != nil || terminalReplay.SettledAt == nil || !terminalReplay.SettledAt.Equal(*settled.SettledAt) {
		t.Fatalf("settled timestamp replay = %#v, %v", terminalReplay, err)
	}
}

func TestGameStartGateAndConfigOrdering(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
	if _, err := trig.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,0)`, game.FishingStandardRTPKey, "broken"); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE site_config SET value='broken' WHERE key=?`, game.GamesEnabledKey); err != nil {
		t.Fatal(err)
	}
	input := StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "ordered"}
	if _, err := trig.service.StartFishingRound(context.Background(), input); !errors.Is(err, ErrGameDisabled) {
		t.Fatalf("malformed switch did not fail closed before economy: %v", err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key=?`, game.GamesEnabledKey); err != nil {
		t.Fatal(err)
	}
	admin, err := trig.store.EnsureAdminUser("game-admin")
	if err != nil {
		t.Fatal(err)
	}
	adminInput := StartFishingInput{UserID: admin.ID, Bait: fishing.BaitWorm, IdempotencyKey: "admin"}
	if _, err := trig.service.StartFishingRound(context.Background(), adminInput); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("administrator was not rejected before economy compile: %v", err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, trig.user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.StartFishingRound(context.Background(), input); !errors.Is(err, ErrBanned) {
		t.Fatalf("account gate did not precede economy compile: %v", err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET is_banned=0 WHERE id=?`, trig.user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.StartFishingRound(context.Background(), input); !errors.Is(err, ErrInvalidSiteConfig) {
		t.Fatalf("active account accepted corrupt economy: %v", err)
	}
}

func TestGameStartLimiterClassifications(t *testing.T) {
	t.Run("feature failures release", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
		if _, err := trig.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key=?`, game.GamesEnabledKey); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < 40; index++ {
			_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("disabled-%d", index),
			})
			if !errors.Is(err, ErrGameDisabled) {
				t.Fatalf("disabled %d = %v", index, err)
			}
		}
		if _, err := trig.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key=?`, game.GamesEnabledKey); err != nil {
			t.Fatal(err)
		}
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "enabled"}); err != nil {
			t.Fatalf("released attempts consumed limiter: %v", err)
		}
	})

	t.Run("pending consumes", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 100_000_000, 0, false)
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "success"}); err != nil {
			t.Fatal(err)
		}
		for index := 1; index < game.FishingStartsPerMinute; index++ {
			_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("pending-%d", index),
			})
			if !errors.Is(err, ErrGamePending) {
				t.Fatalf("pending %d = %v", index, err)
			}
		}
		_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "limited"})
		if !errors.Is(err, ErrGameRateLimited) {
			t.Fatalf("pending outcomes did not consume limiter: %v", err)
		}
	})

	t.Run("rng failures release", func(t *testing.T) {
		trig := newGameTestRig(t, failingIntSource{}, 10_000_000, 0, false)
		for index := 0; index < 40; index++ {
			_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("rng-%d", index),
			})
			if err == nil {
				t.Fatalf("rng failure %d succeeded", index)
			}
		}
		trig.service.outcomeSource = zeroIntSource{}
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "rng-recovered"}); err != nil {
			t.Fatalf("rng failures consumed limiter: %v", err)
		}
	})

	t.Run("insufficient balance consumes", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
		for index := 0; index < game.FishingStartsPerMinute; index++ {
			_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("poor-%d", index),
			})
			if !errors.Is(err, ErrInsufficientCredits) {
				t.Fatalf("insufficient %d = %v", index, err)
			}
		}
		if _, err := trig.store.DB().Exec(`UPDATE users SET credits=10000000 WHERE id=?`, trig.user.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "still-limited"}); !errors.Is(err, ErrGameRateLimited) {
			t.Fatalf("insufficient attempts did not consume limiter: %v", err)
		}
	})
}

func TestGameDeletionBoundaryCanAbortWithoutLosingWindow(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 0, 0, false)
	for index := 0; index < game.FishingStartsPerMinute-1; index++ {
		_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
			UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("delete-window-%d", index),
		})
		if !errors.Is(err, ErrInsufficientCredits) {
			t.Fatalf("seed %d = %v", index, err)
		}
	}
	commit, abort, err := trig.service.BeginUserDeletion(trig.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "during-delete"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("start during deletion = %v", err)
	}
	if !abort() || abort() || commit() {
		t.Fatal("delete abort did not win exactly once")
	}
	if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "last-slot"}); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("abort did not preserve exact limiter window: %v", err)
	}
	if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "over-window"}); !errors.Is(err, ErrGameRateLimited) {
		t.Fatalf("preserved limiter window did not cap: %v", err)
	}
}

func TestGameDeletionAndLateSettlementCannotResurrectState(t *testing.T) {
	for _, deleteFirst := range []bool{true, false} {
		name := "settle-first"
		if deleteFirst {
			name = "delete-first"
		}
		t.Run(name, func(t *testing.T) {
			trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 10}}, 10_000_000, 37, false)
			round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "delete-order",
			})
			if err != nil {
				t.Fatal(err)
			}
			if deleteFirst {
				if _, err := trig.store.DB().Exec(`DELETE FROM users WHERE id=?`, trig.user.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := trig.service.SettleDueGame(context.Background(), round.SettlementID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("late settle after delete = %v", err)
				}
			} else {
				if _, err := trig.service.SettleDueGame(context.Background(), round.SettlementID); err != nil {
					t.Fatal(err)
				}
				if _, err := trig.store.DB().Exec(`DELETE FROM users WHERE id=?`, trig.user.ID); err != nil {
					t.Fatal(err)
				}
			}
			if gameCount(t, trig.store, `SELECT COUNT(*) FROM users WHERE id=?`, trig.user.ID) != 0 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements`) != 0 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_outcomes`) != 0 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_best`) != 0 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 0 {
				t.Fatal("account deletion left or resurrected game state")
			}
		})
	}
}

func TestGameSettleReplayReleaseAndTerminalDoesNotBlockStart(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 55, false)
	ctx := context.Background()
	first, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "first"})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: first.RoundID})
	if err != nil {
		t.Fatal(err)
	}
	if settled.State != GameCommitted || settled.Outcome == nil || settled.Outcome.Tier != fishing.TierJunk || settled.EventSequence != 2 || settled.BalanceMilli != 17_500_000 {
		t.Fatalf("settled = %#v", settled)
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_settlement' AND credits_delta=0 AND donation_credit_delta=0`) != 1 {
		t.Fatal("zero payout did not produce exactly one settlement ledger")
	}
	if _, err := trig.store.ApplyCreditOperation(ctx, CreditOperation{
		Kind: LedgerCheckinAward, UserID: trig.user.ID, OperationID: "sys.checkin.game-test",
		CreditsDelta: 100, CreatedAt: trig.clock.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: first.RoundID})
	if err != nil || !replay.Replayed || replay.BalanceMilli != settled.BalanceMilli+100 || replay.EventSequence != 2 {
		t.Fatalf("settle replay = %#v, %v", replay, err)
	}
	other, err := trig.store.CreateDiscordUser("other-user", "other-user", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: other.ID, RoundID: first.RoundID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong owner = %v", err)
	}

	// Persist a non-zero hypothetical payout, then prove the internal release
	// capability never reveals it in either the first or replay projection.
	trig.service.outcomeSource = &scriptedIntSource{values: []uint64{968, 0, 10}}
	second, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "second"})
	if err != nil {
		t.Fatalf("unacknowledged terminal blocked start: %v", err)
	}
	released, err := trig.service.ReleaseGame(ctx, second.SettlementID)
	if err != nil || released.State != GameReleased || released.PayoutMilli != 0 || released.Outcome != nil || released.BalanceMilli != 17_500_100 {
		t.Fatalf("released = %#v, %v", released, err)
	}
	if replay, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: second.RoundID}); err != nil || replay.State != GameReleased || !replay.Replayed || replay.PayoutMilli != 0 || replay.Outcome != nil {
		t.Fatalf("released settle replay = %#v, %v", replay, err)
	}
	var donation int64
	if err := trig.store.DB().QueryRow(`SELECT donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&donation); err != nil || donation != 55 {
		t.Fatalf("game changed donation credit = %d, %v", donation, err)
	}
}

func TestGameDueRetryAndFeatureClosure(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
	round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "due"})
	if err != nil {
		t.Fatal(err)
	}
	if due, err := trig.service.ListDueGameSettlements(context.Background(), trig.clock.Now(), 100); err != nil || len(due) != 0 {
		t.Fatalf("early due = %#v, %v", due, err)
	}
	trig.clock.Advance(GameAutoSettleDelay)
	due, err := trig.service.ListDueGameSettlements(context.Background(), trig.clock.Now(), 100)
	if err != nil || len(due) != 1 || due[0].SettlementID != round.SettlementID {
		t.Fatalf("due = %#v, %v", due, err)
	}
	for attempt := int64(1); attempt <= 8; attempt++ {
		at := trig.clock.Now()
		if err := trig.service.RecordGameRetryFailure(context.Background(), round.SettlementID, at, GameRetryTransient); err != nil {
			t.Fatal(err)
		}
		var storedAttempt, next int64
		var class string
		if err := trig.store.DB().QueryRow(`SELECT attempt_count,next_attempt_at,last_error_class FROM game_settlements WHERE id=?`, round.SettlementID).
			Scan(&storedAttempt, &next, &class); err != nil {
			t.Fatal(err)
		}
		if storedAttempt != attempt || next != at.Unix()+int64(gameRetryDelay(attempt)/time.Second) || class != string(GameRetryTransient) {
			t.Fatalf("retry %d = count %d next %d class %q", attempt, storedAttempt, next, class)
		}
		trig.clock.Advance(gameRetryDelay(attempt))
	}
	if gameRetryDelay(8) != time.Hour || gameRetryDelay(1000) != time.Hour {
		t.Fatal("retry backoff did not saturate")
	}
	if _, err := trig.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key IN (?,?)`, game.GamesEnabledKey, game.FishingEnabledKey); err != nil {
		t.Fatal(err)
	}
	if _, err := trig.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, trig.user.ID); err != nil {
		t.Fatal(err)
	}
	settled, err := trig.service.SettleDueGame(context.Background(), round.SettlementID)
	if err != nil || settled.State != GameCommitted {
		t.Fatalf("feature closure blocked paid settlement: %#v, %v", settled, err)
	}
	var attempts int64
	var next sql.NullInt64
	var lastAttempt sql.NullInt64
	var class string
	if err := trig.store.DB().QueryRow(`SELECT attempt_count,next_attempt_at,last_attempt_at,last_error_class FROM game_settlements WHERE id=?`, round.SettlementID).
		Scan(&attempts, &next, &lastAttempt, &class); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || next.Valid || lastAttempt.Valid || class != "" {
		t.Fatalf("terminal retry metadata = %d %#v %#v %q", attempts, next, lastAttempt, class)
	}
	if replay, err := trig.service.SettleDueGame(context.Background(), round.SettlementID); err != nil || !replay.Replayed || replay.State != GameCommitted {
		t.Fatalf("terminal settle replay = %#v, %v", replay, err)
	}
	if err := trig.service.RecordGameRetryFailure(context.Background(), round.SettlementID, trig.clock.Now(), GameRetryBusy); err != nil {
		t.Fatalf("terminal retry replay = %v", err)
	}
	if err := trig.store.DB().QueryRow(`SELECT attempt_count,next_attempt_at,last_attempt_at,last_error_class FROM game_settlements WHERE id=?`, round.SettlementID).
		Scan(&attempts, &next, &lastAttempt, &class); err != nil || attempts != 0 || next.Valid || lastAttempt.Valid || class != "" {
		t.Fatalf("terminal retry no-op metadata = %d %#v %#v %q, %v", attempts, next, lastAttempt, class, err)
	}
}

func TestGameReleaseClearsRetryHistoryAndReplays(t *testing.T) {
	trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 10}}, 10_000_000, 0, false)
	round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "retry-release",
	})
	if err != nil {
		t.Fatal(err)
	}
	trig.clock.Advance(GameAutoSettleDelay)
	if err := trig.service.RecordGameRetryFailure(context.Background(), round.SettlementID, trig.clock.Now(), GameRetryBusy); err != nil {
		t.Fatal(err)
	}
	trig.clock.Advance(GameRetryInitialDelay)
	if err := trig.service.RecordGameRetryFailure(context.Background(), round.SettlementID, trig.clock.Now(), GameRetryTransient); err != nil {
		t.Fatal(err)
	}
	released, err := trig.service.ReleaseGame(context.Background(), round.SettlementID)
	if err != nil || released.State != GameReleased || released.PayoutMilli != 0 || released.Outcome != nil {
		t.Fatalf("release after retry = %#v, %v", released, err)
	}
	var attempts int64
	var next, lastAttempt sql.NullInt64
	var class string
	if err := trig.store.DB().QueryRow(`SELECT attempt_count,next_attempt_at,last_attempt_at,last_error_class FROM game_settlements WHERE id=?`, round.SettlementID).
		Scan(&attempts, &next, &lastAttempt, &class); err != nil || attempts != 0 || next.Valid || lastAttempt.Valid || class != "" {
		t.Fatalf("released retry metadata = %d %#v %#v %q, %v", attempts, next, lastAttempt, class, err)
	}
	replay, err := trig.service.ReleaseGame(context.Background(), round.SettlementID)
	if err != nil || !replay.Replayed || replay.State != GameReleased || replay.PayoutMilli != 0 || replay.Outcome != nil {
		t.Fatalf("released replay = %#v, %v", replay, err)
	}
}

func TestGameDueListStableOrderAndBounds(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
	users := []*User{trig.user}
	for index := 1; index < 8; index++ {
		user, err := trig.store.CreateDiscordUser(fmt.Sprintf("due-user-%d", index), fmt.Sprintf("due-user-%d", index), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := trig.store.DB().Exec(`UPDATE users SET credits=10000000 WHERE id=?`, user.ID); err != nil {
			t.Fatal(err)
		}
		users = append(users, user)
	}
	for index, user := range users {
		round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
			UserID: user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("due-order-%d", index),
		})
		if err != nil {
			t.Fatal(err)
		}
		next := round.AutoSettleAt.Unix() - int64((index*7)%3)
		if _, err := trig.store.DB().Exec(`UPDATE game_settlements SET next_attempt_at=? WHERE id=?`, next, round.SettlementID); err != nil {
			t.Fatal(err)
		}
	}
	trig.clock.Advance(GameAutoSettleDelay)
	first, err := trig.service.ListDueGameSettlements(context.Background(), trig.clock.Now(), GameRecoveryBatchMax)
	if err != nil || len(first) != len(users) {
		t.Fatalf("due list = %d, %v", len(first), err)
	}
	for index := 1; index < len(first); index++ {
		previous, current := first[index-1], first[index]
		if current.NextAttempt.Before(previous.NextAttempt) ||
			(current.NextAttempt.Equal(previous.NextAttempt) && current.SettlementID <= previous.SettlementID) {
			t.Fatalf("due order at %d = %#v then %#v", index, previous, current)
		}
	}
	second, err := trig.service.ListDueGameSettlements(context.Background(), trig.clock.Now(), GameRecoveryBatchMax)
	if err != nil || len(second) != len(first) {
		t.Fatalf("repeat due list = %d, %v", len(second), err)
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("due order was unstable at %d: %#v/%#v", index, first[index], second[index])
		}
	}
	prefix, err := trig.service.ListDueGameSettlements(context.Background(), trig.clock.Now(), 3)
	if err != nil || len(prefix) != 3 {
		t.Fatalf("bounded due list = %d, %v", len(prefix), err)
	}
	for index := range prefix {
		if prefix[index] != first[index] {
			t.Fatalf("bounded batch was not stable prefix at %d", index)
		}
	}
	if _, err := trig.service.ListDueGameSettlements(context.Background(), trig.clock.Now(), GameRecoveryBatchMax+1); !errors.Is(err, ErrGameInvalid) {
		t.Fatalf("over-limit due list = %v", err)
	}
}

func TestGameSettlementUsesStartFrozenEconomics(t *testing.T) {
	trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 10}}, 20_000_000, 81, false)
	round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "frozen-economy",
	})
	if err != nil {
		t.Fatal(err)
	}
	var storedEntry, storedPayout int64
	if err := trig.store.DB().QueryRow(`SELECT entry_milli,payout_milli FROM game_settlements WHERE id=?`, round.SettlementID).
		Scan(&storedEntry, &storedPayout); err != nil {
		t.Fatal(err)
	}
	if storedEntry != 2_500_000 || storedPayout <= 0 {
		t.Fatalf("initial snapshot = %d/%d", storedEntry, storedPayout)
	}
	for key, value := range map[string]string{
		game.FishingWormPriceMilliKey: "1000000",
		game.FishingStandardRTPKey:    "50",
	} {
		if _, err := trig.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, trig.clock.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	settled, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: round.RoundID})
	if err != nil {
		t.Fatal(err)
	}
	if settled.EntryMilli != storedEntry || settled.PayoutMilli != storedPayout || settled.BalanceMilli != 20_000_000-storedEntry+storedPayout {
		t.Fatalf("settle re-read changed economics: %#v", settled)
	}
	var donation int64
	if err := trig.store.DB().QueryRow(`SELECT donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&donation); err != nil || donation != 81 {
		t.Fatalf("settle changed cumulative reward = %d, %v", donation, err)
	}
}

func TestGameStartRollbackOnLedgerAndActivityFailure(t *testing.T) {
	t.Run("ledger collision", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 99, true)
		entropy := append(bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)...)
		trig.service.idReader = bytes.NewReader(entropy)
		expectedSettlement, err := generateGameID("gst_", bytes.NewReader(bytes.Repeat([]byte{2}, 16)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := trig.store.DB().Exec(`INSERT INTO credit_ledger
(operation_id,user_id,kind,credits_delta,donation_credit_delta,credits_after,donation_credit_after,created_at)
VALUES (?,?, 'checkin_award',0,0,10000000,99,?)`, gameLedgerOperationID(expectedSettlement, "reserve"), trig.user.ID, trig.clock.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		_, err = trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "collision"})
		if !errors.Is(err, ErrGameLedgerCollision) {
			t.Fatalf("ledger collision = %v", err)
		}
		if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM user_activity_daily`) != 0 {
			t.Fatal("ledger collision left partial game state")
		}
		var credits, donation int64
		if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil || credits != 10_000_000 || donation != 99 {
			t.Fatalf("collision balances = %d/%d %v", credits, donation, err)
		}
	})

	t.Run("activity trigger", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 44, true)
		if _, err := trig.store.DB().Exec(`CREATE TRIGGER fail_game_activity BEFORE INSERT ON user_activity_daily BEGIN SELECT RAISE(ABORT,'activity failure'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "activity-failure"}); err == nil {
			t.Fatal("activity failure unexpectedly succeeded")
		}
		if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 0 {
			t.Fatal("activity failure left round or ledger")
		}
		var credits, donation int64
		if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil || credits != 10_000_000 || donation != 44 {
			t.Fatalf("activity failure balances = %d/%d %v", credits, donation, err)
		}
	})
}

func TestGameTerminalLedgerCollisionRollsBack(t *testing.T) {
	for _, target := range []GameSettlementState{GameCommitted, GameReleased} {
		t.Run(string(target), func(t *testing.T) {
			trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 10}}, 10_000_000, 47, false)
			round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "terminal-ledger-" + string(target),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := trig.service.RecordGameRetryFailure(context.Background(), round.SettlementID, trig.clock.Now(), GameRetryBusy); err != nil {
				t.Fatal(err)
			}
			action := "settle"
			ledgerKind := LedgerGameSettlement
			if target == GameReleased {
				action = "release"
				ledgerKind = LedgerGameRelease
			}
			collisionID := gameLedgerOperationID(round.SettlementID, action)
			collision, err := trig.store.ApplyCreditOperation(context.Background(), CreditOperation{
				Kind: LedgerCheckinAward, UserID: trig.user.ID, OperationID: collisionID,
				CreatedAt: trig.clock.Now(),
			})
			if err != nil || !collision.Applied {
				t.Fatalf("seed collision = %#v, %v", collision, err)
			}
			if target == GameCommitted {
				_, err = trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: round.RoundID})
			} else {
				_, err = trig.service.ReleaseGame(context.Background(), round.SettlementID)
			}
			if !errors.Is(err, ErrGameLedgerCollision) {
				t.Fatalf("terminal ledger collision = %v", err)
			}
			var state, class string
			var finalized, settledAt sql.NullInt64
			var next, lastAttempt sql.NullInt64
			var attempts, eventSequence, credits, donation int64
			if err := trig.store.DB().QueryRow(`SELECT state,finalized_at,next_attempt_at,attempt_count,last_attempt_at,last_error_class FROM game_settlements WHERE id=?`, round.SettlementID).
				Scan(&state, &finalized, &next, &attempts, &lastAttempt, &class); err != nil {
				t.Fatal(err)
			}
			if err := trig.store.DB().QueryRow(`SELECT settled_at,event_seq FROM game_rounds WHERE id=?`, round.RoundID).Scan(&settledAt, &eventSequence); err != nil {
				t.Fatal(err)
			}
			if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil {
				t.Fatal(err)
			}
			if state != string(GameReserved) || finalized.Valid || !next.Valid || attempts != 1 || !lastAttempt.Valid || class != string(GameRetryBusy) ||
				settledAt.Valid || eventSequence != 1 || credits != 7_500_000 || donation != 47 {
				t.Fatalf("collision rollback state = %q/%#v/%#v/%d/%#v/%q round=%#v/%d balances=%d/%d",
					state, finalized, next, attempts, lastAttempt, class, settledAt, eventSequence, credits, donation)
			}
			if gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE operation_id=? AND kind='checkin_award'`, collisionID) != 1 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind=?`, string(ledgerKind)) != 0 ||
				gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_best`) != 0 {
				t.Fatal("terminal ledger collision wrote target ledger or best")
			}
		})
	}
}

func TestGameActivityOverflowRollsBackEconomy(t *testing.T) {
	for _, scope := range []string{"user", "site"} {
		t.Run(scope, func(t *testing.T) {
			trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 29, true)
			at := trig.clock.Now()
			if err := trig.store.RecordActivity(context.Background(), trig.user.ID, at, ActivityDelta{GameRounds: 1}); err != nil {
				t.Fatal(err)
			}
			if scope == "user" {
				if _, err := trig.store.DB().Exec(`UPDATE user_activity_daily SET game_rounds=? WHERE user_id=?`, int64(math.MaxInt64), trig.user.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := trig.store.DB().Exec(`UPDATE site_activity_daily SET game_rounds=?`, int64(math.MaxInt64)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "activity-overflow-" + scope,
			}); err == nil {
				t.Fatal("activity overflow start succeeded")
			}
			if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 0 {
				t.Fatal("activity overflow left round or ledger")
			}
			var credits, donation int64
			if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil || credits != 10_000_000 || donation != 29 {
				t.Fatalf("activity overflow balances = %d/%d, %v", credits, donation, err)
			}
			if scope == "site" && gameCount(t, trig.store, `SELECT game_rounds FROM user_activity_daily WHERE user_id=?`, trig.user.ID) != 1 {
				t.Fatal("site overflow did not roll back user aggregate")
			}
		})
	}
}

func TestGameCommitFailuresRollbackCompleteTransaction(t *testing.T) {
	sentinel := errors.New("injected commit failure")
	realCommit := func(tx *sql.Tx) error { return tx.Commit() }

	t.Run("start", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 61, true)
		trig.service.commitTx = func(*sql.Tx) error { return sentinel }
		_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
			UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "commit-start",
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("start commit failure = %v", err)
		}
		for name, query := range map[string]string{
			"settlement": `SELECT COUNT(*) FROM game_settlements`,
			"round":      `SELECT COUNT(*) FROM game_rounds`,
			"outcome":    `SELECT COUNT(*) FROM game_fishing_outcomes`,
			"ledger":     `SELECT COUNT(*) FROM credit_ledger`,
			"activity":   `SELECT COUNT(*) FROM user_activity_daily`,
		} {
			if count := gameCount(t, trig.store, query); count != 0 {
				t.Fatalf("%s survived failed commit: %d", name, count)
			}
		}
		var credits, donation int64
		if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil || credits != 10_000_000 || donation != 61 {
			t.Fatalf("failed start balances = %d/%d, %v", credits, donation, err)
		}
		trig.service.commitTx = realCommit
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
			UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "commit-recovered",
		}); err != nil {
			t.Fatalf("commit fault consumed limiter: %v", err)
		}
	})

	t.Run("settle", func(t *testing.T) {
		trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 10}}, 10_000_000, 73, false)
		round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
			UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "settle-commit",
		})
		if err != nil {
			t.Fatal(err)
		}
		trig.service.commitTx = func(*sql.Tx) error { return sentinel }
		if _, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: round.RoundID}); !errors.Is(err, sentinel) {
			t.Fatalf("settle commit failure = %v", err)
		}
		if gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_settlement'`) != 0 ||
			gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_best`) != 0 {
			t.Fatal("failed settlement commit left ledger or best")
		}
		var state string
		var settledAt sql.NullInt64
		var credits, donation int64
		if err := trig.store.DB().QueryRow(`SELECT state FROM game_settlements WHERE id=?`, round.SettlementID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if err := trig.store.DB().QueryRow(`SELECT settled_at FROM game_rounds WHERE id=?`, round.RoundID).Scan(&settledAt); err != nil {
			t.Fatal(err)
		}
		if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil {
			t.Fatal(err)
		}
		if state != string(GameReserved) || settledAt.Valid || credits != 7_500_000 || donation != 73 {
			t.Fatalf("failed settle state = %q/%#v/%d/%d", state, settledAt, credits, donation)
		}
		trig.service.commitTx = realCommit
		if settled, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: round.RoundID}); err != nil || settled.State != GameCommitted {
			t.Fatalf("settle after commit fault = %#v, %v", settled, err)
		}
	})
}

func TestGameIDFailuresAndCollisionRollback(t *testing.T) {
	t.Run("short round id entropy", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
		trig.service.idReader = bytes.NewReader([]byte{1})
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "short-round-id"}); err == nil {
			t.Fatal("short round id entropy succeeded")
		}
		if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 0 {
			t.Fatal("round id failure wrote state")
		}
		trig.service.idReader = &countingIDReader{}
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "short-round-recovered"}); err != nil {
			t.Fatalf("round id failure consumed limiter: %v", err)
		}
	})

	t.Run("short settlement id entropy", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 10_000_000, 0, false)
		trig.service.idReader = bytes.NewReader(bytes.Repeat([]byte{1}, 17))
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "short-settlement-id"}); err == nil {
			t.Fatal("short settlement id entropy succeeded")
		}
		if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_outcomes`) != 0 {
			t.Fatal("settlement id failure wrote state")
		}
	})

	t.Run("collision", func(t *testing.T) {
		trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 0, false)
		entropy := append(bytes.Repeat([]byte{7}, 16), bytes.Repeat([]byte{8}, 16)...)
		trig.service.idReader = bytes.NewReader(entropy)
		first, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "id-first"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := trig.service.SettleFishingRound(context.Background(), SettleFishingInput{UserID: trig.user.ID, RoundID: first.RoundID}); err != nil {
			t.Fatal(err)
		}
		trig.service.idReader = bytes.NewReader(entropy)
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "id-collision"}); err == nil {
			t.Fatal("duplicate CSPRNG ids succeeded")
		}
		if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 1 || gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_reserve'`) != 1 {
			t.Fatal("id collision changed economic state")
		}
		trig.service.idReader = &countingIDReader{next: 100}
		if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "id-after-collision"}); err != nil {
			t.Fatalf("id collision consumed limiter: %v", err)
		}
	})
}

func TestGameCancellationRollsBackAndReleasesLimiter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	trig := newGameTestRig(t, &cancelingIntSource{cancel: cancel}, 10_000_000, 19, true)
	_, err := trig.service.StartFishingRound(ctx, StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "cancelled-start",
	})
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("cancelled start = %v, context %v", err, ctx.Err())
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements`) != 0 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_outcomes`) != 0 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 0 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM user_activity_daily`) != 0 {
		t.Fatal("cancelled start left persistent state")
	}
	var credits, donation int64
	if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil || credits != 10_000_000 || donation != 19 {
		t.Fatalf("cancelled balances = %d/%d, %v", credits, donation, err)
	}
	trig.service.outcomeSource = zeroIntSource{}
	if _, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
		UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "cancel-recovered",
	}); err != nil {
		t.Fatalf("cancelled start consumed limiter: %v", err)
	}
}

func TestGameBusyFailureLeavesDatabaseAndLimiterUnchanged(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 20_000_000, 0, false)
	if _, err := trig.store.DB().Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	locker, err := sql.Open("sqlite", trig.path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	if _, err := locker.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	lockTx, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(`UPDATE site_config SET updated_at=updated_at WHERE key=?`, game.GamesEnabledKey); err != nil {
		t.Fatal(err)
	}
	_, startErr := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "busy-start"})
	if startErr == nil || ClassifyGameRetryError(startErr) != GameRetryBusy {
		t.Fatalf("busy start = %v (%s)", startErr, ClassifyGameRetryError(startErr))
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 0 || gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger`) != 0 {
		t.Fatal("busy start changed persistent state")
	}
	if err := lockTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "after-busy"})
	if err != nil {
		t.Fatalf("busy failure consumed limiter: %v", err)
	}
	trig.clock.Advance(GameAutoSettleDelay)

	lockTx, err = locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(`UPDATE site_config SET updated_at=updated_at WHERE key=?`, game.GamesEnabledKey); err != nil {
		t.Fatal(err)
	}
	retryErr := trig.service.RecordGameRetryFailure(context.Background(), round.SettlementID, trig.clock.Now(), GameRetryBusy)
	if retryErr == nil || ClassifyGameRetryError(retryErr) != GameRetryBusy {
		t.Fatalf("busy retry update = %v (%s)", retryErr, ClassifyGameRetryError(retryErr))
	}
	var attempts int64
	var last sql.NullInt64
	if err := trig.store.DB().QueryRow(`SELECT attempt_count,last_attempt_at FROM game_settlements WHERE id=?`, round.SettlementID).Scan(&attempts, &last); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || last.Valid {
		t.Fatalf("busy retry changed metadata = %d/%#v", attempts, last)
	}
	if err := lockTx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestGameConcurrentDifferentKeysCreateOnePendingRound(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 100_000_000, 0, true)
	const callers = 16
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("different-%d", index),
			})
			errs <- err
		}(index)
	}
	wg.Wait()
	close(errs)
	succeeded, pending := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrGamePending):
			pending++
		default:
			t.Fatalf("different-key start = %v", err)
		}
	}
	if succeeded != 1 || pending != callers-1 {
		t.Fatalf("different-key results = success %d pending %d", succeeded, pending)
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 1 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_reserve'`) != 1 ||
		gameCount(t, trig.store, `SELECT game_rounds FROM user_activity_daily WHERE user_id=?`, trig.user.ID) != 1 {
		t.Fatal("different-key race duplicated round, debit, or activity")
	}
}

func TestGameConcurrentSameKeyDifferentBaitIsSuccessAndConflict(t *testing.T) {
	trig := newGameTestRig(t, zeroIntSource{}, 100_000_000, 0, true)
	type result struct {
		round FishingRound
		err   error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, bait := range []fishing.Bait{fishing.BaitWorm, fishing.BaitLure} {
		wg.Add(1)
		go func(bait fishing.Bait) {
			defer wg.Done()
			round, err := trig.service.StartFishingRound(context.Background(), StartFishingInput{
				UserID: trig.user.ID, Bait: bait, IdempotencyKey: "same-key-different-bait",
			})
			results <- result{round: round, err: err}
		}(bait)
	}
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	var winningBait fishing.Bait
	for item := range results {
		switch {
		case item.err == nil:
			succeeded++
			winningBait = item.round.Bait
		case errors.Is(item.err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("same-key different-bait result = %#v, %v", item.round, item.err)
		}
	}
	if succeeded != 1 || conflicted != 1 || (winningBait != fishing.BaitWorm && winningBait != fishing.BaitLure) {
		t.Fatalf("same-key different-bait totals = success %d conflict %d winner %q", succeeded, conflicted, winningBait)
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 1 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM game_settlements`) != 1 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_reserve'`) != 1 ||
		gameCount(t, trig.store, `SELECT game_rounds FROM user_activity_daily WHERE user_id=?`, trig.user.ID) != 1 {
		t.Fatal("same-key different-bait race duplicated economic state")
	}
}

func TestGameConcurrentStartSettleAndDonorReward(t *testing.T) {
	trig := newGameTestRig(t, &scriptedIntSource{values: []uint64{968, 0, 10}}, 100_000_000, 0, false)
	ctx := context.Background()
	const callers = 24
	results := make(chan FishingRound, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			round, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: "same-key"})
			results <- round
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	var roundID, settlementID string
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if roundID == "" {
			roundID = result.RoundID
			settlementID = result.SettlementID
		} else if result.RoundID != roundID {
			t.Fatalf("concurrent replay ids = %q/%q", roundID, result.RoundID)
		} else if result.SettlementID != settlementID {
			t.Fatalf("concurrent replay settlement ids = %q/%q", settlementID, result.SettlementID)
		}
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM game_rounds`) != 1 || gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_reserve'`) != 1 {
		t.Fatal("concurrent start duplicated economic state")
	}
	var payout int64
	if err := trig.store.DB().QueryRow(`SELECT payout_milli FROM game_settlements WHERE id=?`, settlementID).Scan(&payout); err != nil || payout <= 0 {
		t.Fatalf("concurrent persisted payout = %d, %v", payout, err)
	}

	settleErrs := make(chan error, callers+2)
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func(worker bool) {
			defer wg.Done()
			var err error
			if worker {
				_, err = trig.service.SettleDueGame(ctx, settlementID)
			} else {
				_, err = trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: roundID})
			}
			settleErrs <- err
		}(index%2 == 0)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := trig.store.ApplyCreditOperation(ctx, CreditOperation{
			Kind: LedgerDonorReward, UserID: trig.user.ID, OperationID: "sys.donor.game-race",
			CreditsDelta: 1234, DonationCreditDelta: 1234, CreatedAt: trig.clock.Now(),
		})
		settleErrs <- err
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := trig.store.ApplyCreditOperation(ctx, CreditOperation{
			Kind: LedgerCheckinAward, UserID: trig.user.ID, OperationID: "sys.checkin.game-race",
			CreditsDelta: 100, CreatedAt: trig.clock.Now(),
		})
		settleErrs <- err
	}()
	wg.Wait()
	close(settleErrs)
	for err := range settleErrs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if gameCount(t, trig.store, `SELECT COUNT(*) FROM credit_ledger WHERE kind='game_settlement'`) != 1 ||
		gameCount(t, trig.store, `SELECT COUNT(*) FROM game_fishing_best`) != 1 {
		t.Fatal("concurrent settle duplicated ledger or best")
	}
	var credits, donation int64
	if err := trig.store.DB().QueryRow(`SELECT credits,donation_credit FROM users WHERE id=?`, trig.user.ID).Scan(&credits, &donation); err != nil {
		t.Fatal(err)
	}
	if credits != 100_000_000-2_500_000+payout+1_234+100 || donation != 1234 {
		t.Fatalf("concurrent final balances = %d/%d", credits, donation)
	}
}

func TestGameBestOnlyStrictlyLargerFish(t *testing.T) {
	source := &scriptedIntSource{values: []uint64{
		968, 0, 10,
		968, 0, 10,
		968, 0, 11,
	}}
	trig := newGameTestRig(t, source, 100_000_000, 0, false)
	ctx := context.Background()
	var roundIDs []string
	var bestCreatedAt int64
	for index := 0; index < 3; index++ {
		round, err := trig.service.StartFishingRound(ctx, StartFishingInput{UserID: trig.user.ID, Bait: fishing.BaitWorm, IdempotencyKey: fmt.Sprintf("fish-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		roundIDs = append(roundIDs, round.RoundID)
		if index == 0 || index == 2 {
			bestCreatedAt = round.CreatedAt.Unix()
		}
		trig.clock.Advance(time.Minute)
		if _, err := trig.service.SettleFishingRound(ctx, SettleFishingInput{UserID: trig.user.ID, RoundID: round.RoundID}); err != nil {
			t.Fatal(err)
		}
		var bestRound string
		var size int
		var caughtAt int64
		if err := trig.store.DB().QueryRow(`SELECT round_id,size_cm,caught_at FROM game_fishing_best WHERE user_id=?`, trig.user.ID).Scan(&bestRound, &size, &caughtAt); err != nil {
			t.Fatal(err)
		}
		if caughtAt != bestCreatedAt {
			t.Fatalf("best caught_at followed settle delay: got %d want %d", caughtAt, bestCreatedAt)
		}
		if index < 2 && (bestRound != roundIDs[0] || size != 15) {
			t.Fatalf("equal best replaced: %q %d", bestRound, size)
		}
		if index == 2 && (bestRound != roundIDs[2] || size != 16) {
			t.Fatalf("larger best not stored: %q %d", bestRound, size)
		}
	}
}
