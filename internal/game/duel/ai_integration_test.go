package duel_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/strategy"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

type brokenSource struct{ strategy.Source }

func (brokenSource) Decide(context.Context, ai.Request) (ai.Result, error) {
	return ai.Result{}, errors.New("source unavailable")
}

type brokenAdapter struct{ bidding.AIAdapter }

func (brokenAdapter) Source() ai.Source { return brokenSource{} }

func aiFixture(t *testing.T, fallback bool) (*fixture, duel.AIBot) {
	t.Helper()
	f := adminFixture(t, "bidding")
	if fallback {
		if err := f.s.Close(); err != nil {
			t.Fatal(err)
		}
		f.options.AI = brokenAdapter{}
		f.options.ReportError = func(err error) { t.Logf("AI callback: %v", err) }
		var err error
		f.s, err = bidding.New(f.options)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	state, err := f.s.AdminAI(adminContext())
	if err != nil {
		t.Fatal(err)
	}
	if state.Settings.Enabled || len(state.Policies) != 4 || len(state.Bots) != 4 || state.Bots[0].Enabled || state.Bots[0].Ticket != "0" || state.Bots[0].FirstReward != "0" {
		t.Fatalf("unsafe defaults: %+v", state)
	}
	if _, err := f.s.SetAIEnabled(adminContext(), f.key(), duel.AISettings{Enabled: true, Revision: state.Settings.Revision}); err != nil {
		t.Fatal(err)
	}
	bot := state.Bots[0]
	bot.Enabled = true
	return f, f.saveBot(bot, false)
}
func (f *fixture) saveBot(b duel.AIBot, newChallenge bool) duel.AIBot {
	f.t.Helper()
	result, err := f.s.SaveAIBot(adminContext(), f.key(), duel.SaveAIBot{ID: b.ID, ExpectedRevision: b.Revision, Name: b.Name, Description: b.Description, Enabled: b.Enabled, PolicyID: b.PolicyID, PolicyVersion: b.PolicyVersion, Ticket: b.Ticket, FirstReward: b.FirstReward, MemoryDays: b.MemoryDays, MemoryGames: b.MemoryGames, NewChallenge: newChallenge})
	if err != nil {
		f.t.Fatal(err)
	}
	var out duel.AIBot
	if err := json.Unmarshal(result.Body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}
func (f *fixture) enqueueAI(bot string) duel.Queue {
	f.t.Helper()
	offers, err := f.s.ReadAI(f.ctx, f.identity(0))
	if err != nil {
		f.t.Fatal(err)
	}
	for _, offer := range offers.Bots {
		if offer.Terms.AI.BotID == bot {
			in := duel.EnqueueInput{Identity: f.identity(0), IdempotencyKey: f.key(), Mode: "ai", BotID: bot, ExpectedTermsHash: offer.TermsHash}
			if _, err := f.s.Enqueue(f.ctx, in); err != nil {
				f.t.Fatal(err)
			}
			replay, err := f.s.Enqueue(f.ctx, in)
			if err != nil || !replay.Replayed {
				f.t.Fatalf("queue replay: %+v %v", replay, err)
			}
			q := f.read(0).Queue
			if q == nil {
				f.t.Fatal("missing unpaid queue")
			}
			return *q
		}
	}
	f.t.Fatal("missing offer")
	return duel.Queue{}
}
func (f *fixture) aiMatch(bot string) duel.State {
	f.t.Helper()
	f.enqueueAI(bot)
	f.tick()
	v := f.read(0).Current
	if v == nil {
		f.t.Fatalf("no AI match: %+v", f.read(0))
	}
	return *v
}

// With a failed source, bidding 2..13 then 1 wins the first twelve rounds
// against the rule fallback, independent of reward deck and seat order.
func (f *fixture) completeAI() *duel.ResultSummary {
	f.t.Helper()
	// A complete match crosses many asynchronous SQL commits under race.
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		f.tick()
		home := f.read(0)
		if home.Current == nil && home.LatestResult != nil {
			return home.LatestResult
		}
		v := home.Current
		if v == nil {
			f.t.Fatal("lost AI match")
		}
		if v.Locked[v.You] {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var view engine.View
		if err := json.Unmarshal(v.View, &view); err != nil {
			f.t.Fatal(err)
		}
		body := `{"kind":"joker","use":false}`
		if v.Phase == "bid" {
			cards := view.HandRemaining[v.You]
			card := cards[0]
			if len(cards) > 1 {
				card = cards[1]
			}
			body = fmt.Sprintf(`{"kind":"bid","card":%d}`, card)
		}
		f.clock.Add(1)
		f.action(0, *v, body)
	}
	f.t.Fatal("AI match did not complete")
	return nil
}
func countAI(t *testing.T, f *fixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAIUnpaidQueueMixedPaymentFrozenTermsAndRestartRefund(t *testing.T) {
	f, bot := aiFixture(t, false)
	bot.Ticket = "5"
	bot.FirstReward = "2"
	bot = f.saveBot(bot, false)
	q := f.enqueueAI(bot.ID)
	if q.Payment.General != "0" || q.Payment.Game != "0" || q.Position != 1 {
		t.Fatalf("charged queue: %+v", q)
	}
	if n := countAI(t, f, `SELECT count(*) FROM credit_operations WHERE kind='ai_ticket'`); n != 0 {
		t.Fatal(n)
	}
	// A config edit cannot silently change an already accepted offer.
	bot.Ticket = "7"
	bot = f.saveBot(bot, false)
	f.tick()
	v := f.read(0).Current
	if v == nil || v.Ticket != "5" || v.OwnPayment.Game != "3" || v.OwnPayment.General != "2" || v.AI.Terms.FirstReward != "2" {
		t.Fatalf("unfrozen terms or wrong debit: %+v", v)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s, err = bidding.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	result := f.read(0).LatestResult
	if result == nil || result.Outcome != "system_cancelled" || result.OwnRefund.General != "2" || result.OwnRefund.Game != "3" {
		t.Fatalf("refund: %+v", result)
	}
	if n := countAI(t, f, `SELECT count(*) FROM credit_operations WHERE kind='ai_terminal'`); n != 1 {
		t.Fatal("duplicate refund", n)
	}
	if n := countAI(t, f, `SELECT count(*) FROM game_ai_memories`); n != 0 {
		t.Fatal("cancelled sample", n)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAIFallbackWinFirstClearMemoryOffAndExport(t *testing.T) {
	f, bot := aiFixture(t, true)
	bot.Ticket = "1"
	bot.FirstReward = "2"
	bot = f.saveBot(bot, false)
	if _, err := f.s.SetAIPreference(f.ctx, f.identity(0), f.key(), duel.AIPreference{BotID: bot.ID, MemoryEnabled: false}); err != nil {
		t.Fatal(err)
	}
	v := f.aiMatch(bot.ID)
	result := f.completeAI()
	if result.Outcome != "win" || result.AI == nil || !result.AI.FirstClear || result.AI.Reward != "2" || result.OwnRefund.General != "0" || result.OwnRefund.Game != "0" {
		t.Fatalf("fallback settlement: %+v", result)
	}
	detail, err := f.s.HistoryDetail(f.ctx, f.identity(0), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	fallback, joker := false, false
	for _, source := range detail.Sources {
		fallback = fallback || source.Origin == "fallback"
		joker = joker || source.Phase == "joker"
	}
	if !fallback || !joker {
		t.Fatal("missing per-phase provenance")
	}
	if n := countAI(t, f, `SELECT count(*) FROM game_ai_memories WHERE user_id=?`, f.users[0]); n != 1 {
		t.Fatal("disabled decision input must still learn", n)
	}
	if n := countAI(t, f, `SELECT count(*) FROM game_onboarding_completions WHERE user_id=?`, f.users[0]); n != 0 {
		t.Fatal("AI advanced onboarding", n)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, _, err := f.s.ExportTx(f.ctx, tx, f.users[0], f.clock.Load(), 1000)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	e := exported.(duel.Export)
	if e.AI == nil || len(e.AI.Memories) != 1 || len(e.AI.Clears) != 1 || len(e.AI.Snapshots) != 1 || len(e.AI.Preferences) != 1 {
		t.Fatalf("incomplete AI export: %+v", e.AI)
	}
	raw, _ := json.Marshal(e)
	for _, private := range []string{"random_seed", "definition_json", "decision_id"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private export", private)
		}
	}
	if _, err := f.s.SetAIPreference(f.ctx, f.identity(0), f.key(), duel.AIPreference{BotID: bot.ID, MemoryEnabled: true}); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(15)
	bot.Name = "Changed name"
	bot.FirstReward = "3"
	bot = f.saveBot(bot, false)
	next := f.aiMatch(bot.ID)
	if !next.AI.MemoryEnabled || next.AI.MemorySamples != 1 {
		t.Fatalf("memory not restored: %+v", next.AI)
	}
	again := f.completeAI()
	if again.AI.FirstClear || again.AI.Reward != "0" {
		t.Fatalf("duplicate first clear: %+v", again.AI)
	}
	bot.MemoryGames = 1
	bot = f.saveBot(bot, false)
	if n := countAI(t, f, `SELECT count(*) FROM game_ai_memories WHERE user_id=?`, f.users[0]); n != 1 {
		t.Fatal("window not trimmed", n)
	}
	bot.MemoryGames = 30
	f.saveBot(bot, false)
	if n := countAI(t, f, `SELECT count(*) FROM game_ai_memories WHERE user_id=?`, f.users[0]); n != 1 {
		t.Fatal("window expansion rebuilt samples", n)
	}
}

func TestAIFreeHistoryDeletionAndUnpaidCancellation(t *testing.T) {
	f, bot := aiFixture(t, true)
	q := f.enqueueAI(bot.ID)
	in := duel.CancelInput{Identity: f.identity(0), IdempotencyKey: f.key(), QueueID: q.ID, ExpectedRevision: "1"}
	if _, err := f.s.CancelQueue(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	if r, err := f.s.CancelQueue(f.ctx, in); err != nil || !r.Replayed {
		t.Fatalf("cancel replay %v", err)
	}
	f.clock.Add(15)
	v := f.aiMatch(bot.ID)
	result := f.completeAI()
	if !result.AI.FirstClear || result.AI.Reward != "0" {
		t.Fatal("zero-reward clear missing")
	}
	if n := countAI(t, f, `SELECT count(*) FROM credit_operations WHERE kind IN ('ai_ticket','ai_terminal')`); n != 0 {
		t.Fatal("zero-value ledger rows", n)
	}
	page, err := f.s.AdminHistory(adminContext(), duel.AdminPageInput{Dataset: "recent"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("free history %+v %v", page, err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], f.clock.Load())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	done.Commit()
	if n := countAI(t, f, `SELECT count(*) FROM game_ai_memories`); n != 0 {
		t.Fatal("memory survived deletion", n)
	}
	var raw string
	if err := f.db.QueryRow(`SELECT header_json FROM game_duel_anonymous WHERE mode='ai'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{v.ID, bot.ID, bot.ChallengeID, `"accepted_at"`, `"phase_seq"`, `"memory"`, `"random_seed"`} {
		if strings.Contains(raw, private) {
			t.Fatal("anonymous linkage", private)
		}
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAIInsufficientAdmissionAndSharedUserSlot(t *testing.T) {
	f, bot := aiFixture(t, false)
	bot.Ticket = "100"
	bot = f.saveBot(bot, false)
	f.enqueueAI(bot.ID)
	c, _ := f.rules.Catalog(f.mode)
	body, _ := json.Marshal(duel.Terms{Game: "bidding", Mode: f.mode, Ticket: "5", RulesVersion: 1, ContentHash: c.Hash})
	hash := sha256.Sum256(body)
	if _, err := f.s.Enqueue(f.ctx, duel.EnqueueInput{Identity: f.identity(0), IdempotencyKey: f.key(), Mode: f.mode, ExpectedTermsHash: hex.EncodeToString(hash[:]), DeviceToken: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("AI and PvP occupied the same user slot", err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	home, err := f.s.HomeSummaryTx(f.ctx, tx, f.users[0])
	tx.Rollback()
	if err != nil || len(home.Continue) != 1 || !strings.HasPrefix(home.Continue[0].ResourceID, "aiq_") {
		t.Fatalf("AI continuation %+v %v", home, err)
	}
	f.tick()
	state := f.read(0)
	if state.Current != nil || state.Queue != nil || state.AIQueueError != "insufficient_credits" {
		t.Fatalf("insufficient admission: %+v", state)
	}
	if n := countAI(t, f, `SELECT count(*) FROM credit_operations WHERE kind='ai_ticket'`); n != 0 {
		t.Fatal("charged failed admission")
	}
	if n := countAI(t, f, `SELECT count(*) FROM game_duel_user_slots WHERE user_id=?`, f.users[0]); n != 0 {
		t.Fatal("failed queue retained slot")
	}
	if _, err := f.s.AdminAI(f.ctx); !errors.Is(err, duel.ErrForbidden) {
		t.Fatal("user obtained admin policies", err)
	}
	f.clock.Add(15)
	bot.Ticket = "0"
	bot = f.saveBot(bot, false)
	f.enqueueAI(bot.ID)
	if _, err := f.s.Enqueue(f.ctx, duel.EnqueueInput{Identity: f.identity(0), IdempotencyKey: f.key(), Mode: "ai", BotID: bot.ID, ExpectedTermsHash: strings.Repeat("0", 64)}); !errors.Is(err, duel.ErrConflict) {
		t.Fatal("duplicate occupied entry", err)
	}
	tx, err = f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.s.CancelUserTx(f.ctx, tx, f.users[0], f.clock.Load())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	done.Commit()
	if f.read(0).Queue != nil {
		t.Fatal("disabled account remained queued")
	}
}

func TestAIAdmissionCapacityFIFOAndExpiry(t *testing.T) {
	f, bot := aiFixture(t, false)
	drain := func() {
		ctx, cancel := context.WithTimeout(f.ctx, time.Minute)
		defer cancel()
		for {
			result, err := f.s.Tick(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !result.More {
				return
			}
		}
	}
	users := addAIUsers(t, f, duel.AIQueueCapacity+1)
	for _, user := range users[:duel.AIQueueCapacity] {
		f.users[0] = user
		f.enqueueAI(bot.ID)
	}
	f.users[0] = users[duel.AIQueueCapacity]
	offers, err := f.s.ReadAI(f.ctx, f.identity(0))
	if err != nil {
		t.Fatal(err)
	}
	in := duel.EnqueueInput{Identity: f.identity(0), IdempotencyKey: f.key(), Mode: "ai", BotID: bot.ID, ExpectedTermsHash: offers.Bots[0].TermsHash}
	if _, err := f.s.Enqueue(f.ctx, in); !errors.Is(err, duel.ErrResourceLimit) {
		t.Fatal("queue capacity", err)
	}
	drain()
	if got := countAI(t, f, "SELECT count(*) FROM game_duel_sessions WHERE state='active' AND economy='ai_challenge'"); got != duel.AIActiveCapacity {
		t.Fatal("active capacity", got)
	}
	for i, user := range users[:duel.AIQueueCapacity] {
		active := countAI(t, f, "SELECT count(*) FROM game_duel_user_slots WHERE user_id=? AND session_id IS NOT NULL", user)
		if (active == 1) != (i < duel.AIActiveCapacity) {
			t.Fatal("FIFO admission", i, active)
		}
	}
	f.clock.Add(121)
	drain()
	if n := countAI(t, f, "SELECT count(*) FROM game_ai_queue WHERE state='waiting'"); n != 0 {
		t.Fatal("expired queue", n)
	}
	if n := countAI(t, f, "SELECT count(*) FROM game_ai_queue WHERE failure='expired'"); n != duel.AIQueueCapacity-duel.AIActiveCapacity {
		t.Fatal("expiry receipts", n)
	}
	if n := countAI(t, f, "SELECT count(*) FROM credit_operations WHERE kind='ai_ticket'"); n != 0 {
		t.Fatal("free admission charged", n)
	}
}

func TestAIFreeAdmissionCanIssueFirstClearReward(t *testing.T) {
	f, bot := aiFixture(t, true)
	bot.FirstReward = "2"
	bot = f.saveBot(bot, false)
	f.aiMatch(bot.ID)
	result := f.completeAI()
	if result.AI == nil || !result.AI.FirstClear || result.AI.Reward != "2" {
		t.Fatalf("free reward %+v", result)
	}
	if n := countAI(t, f, "SELECT count(*) FROM credit_operations WHERE kind='ai_ticket'"); n != 0 {
		t.Fatal("free ticket", n)
	}
	if n := countAI(t, f, "SELECT count(*) FROM credit_operations WHERE kind='ai_terminal'"); n != 1 {
		t.Fatal("reward operation", n)
	}
	if err := f.s.ValidatePersistedState(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAIQueuedPolicyRemainsFrozenAndRespectsDisabling(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprint(disable), func(t *testing.T) {
			f, bot := aiFixture(t, false)
			original := bot.PolicyID
			f.enqueueAI(bot.ID)
			state, err := f.s.AdminAI(adminContext())
			if err != nil {
				t.Fatal(err)
			}
			var accepted duel.AIPolicy
			for _, p := range state.Policies {
				if p.ID == original {
					accepted = p
				} else {
					bot.PolicyID = p.ID
					bot.PolicyVersion = p.Version
				}
			}
			bot = f.saveBot(bot, false)
			if disable {
				_, err = f.s.SaveAIPolicy(adminContext(), f.key(), duel.SaveAIPolicy{ID: accepted.ID, ExpectedRevision: accepted.Revision, Name: accepted.Name, Description: accepted.Description, Enabled: false, Definition: accepted.Definition})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.tick()
			home := f.read(0)
			if disable {
				if home.Current != nil || home.Queue != nil || home.AIQueueError != "closed" {
					t.Fatalf("disabled accepted policy %+v", home)
				}
			} else if home.Current == nil || home.Current.AI.Terms.PolicyID != original || home.Current.AI.Terms.PolicyVersion != accepted.Version {
				t.Fatalf("queue policy changed %+v", home)
			}
		})
	}
}
