package duel_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/gwent"
	gwentengine "github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type passingGwentSource struct{ ai.Source }

func (s passingGwentSource) Decide(ctx context.Context, r ai.Request) (ai.Result, error) {
	if err := ctx.Err(); err != nil {
		return ai.Result{}, err
	}
	choices := r.Actions.(ai.Choices)
	for _, c := range choices {
		a := c.Action.(gwentengine.Action)
		if a.Kind == "continue" || a.Kind == "pass" {
			return ai.Selected(r, c.ID), nil
		}
	}
	return ai.Selected(r, choices[0].ID), nil
}

type passingGwentAdapter struct{ gwent.AIAdapter }

func (a passingGwentAdapter) Source() ai.Source { return passingGwentSource{a.AIAdapter.Source()} }

type brokenGwentSource struct{ ai.Source }

func (s brokenGwentSource) Decide(context.Context, ai.Request) (ai.Result, error) {
	return ai.Result{}, errors.New("unavailable")
}

type brokenGwentAdapter struct{ gwent.AIAdapter }

func (a brokenGwentAdapter) Source() ai.Source { return brokenGwentSource{a.AIAdapter.Source()} }
func gwentAIFixture(t *testing.T, adapter duel.AIAdapter, observeErrors ...func(error)) (*fixture, duel.AIBot) {
	t.Helper()
	f := adminFixture(t, "gwent")
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	f.options.AI = adapter
	f.options.ReportError = func(err error) {
		// A bounded SQL commit may expire under contention; the next Tick
		// retries the still-current window. Completion assertions remain required.
		if errors.Is(err, context.DeadlineExceeded) {
			t.Logf("AI callback retry: %v", err)
		} else {
			t.Errorf("AI callback: %v", err)
		}
		for _, observe := range observeErrors {
			observe(err)
		}
	}
	var err error
	f.s, err = duel.New(f.options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	state, err := f.s.AdminAI(adminContext())
	if err != nil {
		t.Fatal(err)
	}
	if state.Settings.Enabled || len(state.Bots) != 4 || len(state.Policies) != 4 {
		t.Fatal("unsafe defaults", state)
	}
	if _, err = f.s.SetAIEnabled(adminContext(), f.key(), duel.AISettings{Enabled: true, Revision: state.Settings.Revision}); err != nil {
		t.Fatal(err)
	}
	bot := state.Bots[0]
	bot.Enabled = true
	return f, f.saveBot(bot, false)
}
func (f *fixture) enqueueGwentAI(bot duel.AIBot, deck gwentengine.Deck) duel.Queue {
	f.t.Helper()
	offers, err := f.s.ReadAI(f.ctx, f.identity(0))
	if err != nil {
		f.t.Fatal(err)
	}
	for _, offer := range offers.Bots {
		if offer.Terms.AI.BotID == bot.ID {
			if offer.MemoryEnabled || offer.MemorySamples != 0 || len(offer.Terms.AI.BotLoadout) == 0 {
				f.t.Fatal("missing immutable bot deck or unwanted memory", offer)
			}
			loadout, _ := json.Marshal(deck)
			in := duel.EnqueueInput{Identity: f.identity(0), IdempotencyKey: f.key(), Mode: "ai", BotID: bot.ID, ExpectedTermsHash: offer.TermsHash, Loadout: loadout}
			if _, err := f.s.Enqueue(f.ctx, in); err != nil {
				f.t.Fatal(err)
			}
			if replay, err := f.s.Enqueue(f.ctx, in); err != nil || !replay.Replayed {
				f.t.Fatal("queue replay", replay, err)
			}
			q := f.read(0).Queue
			if q == nil || !strings.HasPrefix(q.ID, "gaq_") {
				f.t.Fatal("isolated queue prefix", q)
			}
			tx, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				f.t.Fatal(err)
			}
			summary, err := f.s.HomeSummaryTx(f.ctx, tx, f.users[0])
			tx.Rollback()
			if err != nil || len(summary.Continue) != 1 || summary.Continue[0].ResourceID != q.ID || summary.Continue[0].Game != "gwent" || summary.Continue[0].State != "waiting" || summary.Continue[0].RouteID != "game-gwent" {
				f.t.Fatalf("Gwent AI continuation %+v %v", summary, err)
			}
			return *q
		}
	}
	f.t.Fatal("missing challenge offer")
	return duel.Queue{}
}
func (f *fixture) completeGwentAI() *duel.ResultSummary {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		f.tick()
		home := f.read(0)
		if home.Current == nil && home.LatestResult != nil {
			return home.LatestResult
		}
		if home.Current == nil {
			f.t.Fatal("lost Gwent session")
		}
		v := home.Current
		if v.Locked[v.You] {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		var view gwentengine.View
		if err := json.Unmarshal(v.View, &view); err != nil {
			f.t.Fatal(err)
		}
		selected := view.Legal[0]
		if view.Phase == "mulligan" {
			for _, a := range view.Legal {
				if a.Kind == "continue" {
					selected = a
				}
			}
		}
		if view.Phase == "turn" {
			lead := 0
			for _, row := range view.Board {
				if row.Side == "self" {
					lead += row.Total
				} else {
					lead -= row.Total
				}
			}
			if lead > 0 {
				for _, a := range view.Legal {
					if a.Kind == "pass" {
						selected = a
					}
				}
			} else {
				for _, a := range view.Legal {
					if a.Kind == "play" {
						for _, c := range view.Hand {
							if c.InstanceID == a.Card && c.Power > 0 {
								spy := false
								for _, ability := range c.Abilities {
									if ability == "spy" {
										spy = true
									}
								}
								if !spy {
									selected = a
									break
								}
							}
						}
						if selected.Kind == "play" {
							break
						}
					}
				}
			}
		}
		f.clock.Add(1)
		body, _ := json.Marshal(selected)
		_, err := f.s.Action(f.ctx, duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: v.ID, PhaseSeq: v.PhaseSeq, DecisionID: v.DecisionID, Action: body})
		if err != nil && !errors.Is(err, duel.ErrConflict) {
			f.t.Fatal(err)
		}
	}
	f.t.Fatal("Gwent AI did not complete")
	return nil
}
func TestGwentAIFreeQueueFrozenDeckFirstClearAndRepeatedWin(t *testing.T) {
	f, bot := gwentAIFixture(t, passingGwentAdapter{})
	bot.Ticket = "5"
	bot.FirstReward = "2"
	bot = f.saveBot(bot, false)
	deck, _ := gwentengine.PresetDeck("gemini", "standard-bond")
	f.enqueueGwentAI(bot, deck)
	if n := countAI(t, f, "SELECT count(*) FROM credit_operations WHERE kind='ai_ticket'"); n != 0 {
		t.Fatal("queue charged")
	}
	bot.Ticket = "9"
	bot.FirstReward = "4"
	bot = f.saveBot(bot, false)
	f.tick()
	current := f.read(0).Current
	if current == nil || current.Ticket != "5" {
		t.Fatal("terms not frozen", current)
	}
	var view gwentengine.View
	_ = json.Unmarshal(current.View, &view)
	if view.Self.Faction != "gemini" {
		t.Fatal("human deck lost", view)
	}
	first := f.completeGwentAI()
	if first.Outcome != "win" || first.AI == nil || !first.AI.FirstClear || first.AI.Reward != "2" || first.OwnPayment.Game != "3" || first.OwnPayment.General != "2" {
		t.Fatal("first clear economy", first)
	}
	if n := countAI(t, f, "SELECT count(*) FROM game_duel_results WHERE game_key='gwent'"); n != 0 {
		t.Fatal("AI affected PvP rating")
	}
	bot.Ticket = "0"
	bot.FirstReward = "7"
	bot = f.saveBot(bot, false)
	f.clock.Add(5)
	f.enqueueGwentAI(bot, deck)
	second := f.completeGwentAI()
	if second.AI == nil || second.AI.FirstClear || second.AI.Reward != "0" {
		t.Fatal("repeated first clear", second)
	}
	if n := countAI(t, f, "SELECT count(*) FROM game_ai_clears WHERE challenge_id=?", bot.ChallengeID); n != 1 {
		t.Fatal(n)
	}
	if n := countAI(t, f, "SELECT count(*) FROM game_ai_memories"); n != 0 {
		t.Fatal("unexpected memory")
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
	archive := exported.(duel.Export)
	if archive.AI == nil || len(archive.AI.Clears) != 1 || len(archive.AI.Memories) != 0 || len(archive.History) != 2 {
		t.Fatal("incomplete Gwent export", archive.AI)
	}
	if len(archive.History[0].Detail.Result.AI.Terms.BotLoadout) == 0 {
		t.Fatal("frozen bot deck lost from export")
	}
	f.ledger()
}
func TestGwentAIZeroRewardClearAndSystemRefund(t *testing.T) {
	f, bot := gwentAIFixture(t, passingGwentAdapter{})
	deck, _ := gwentengine.PresetDeck("openai", "standard-balanced")
	f.enqueueGwentAI(bot, deck)
	result := f.completeGwentAI()
	if !result.AI.FirstClear || result.AI.Reward != "0" {
		t.Fatal("zero reward lost clear", result)
	}
	bot.Ticket = "5"
	bot = f.saveBot(bot, false)
	f.clock.Add(1)
	f.enqueueGwentAI(bot, deck)
	f.tick()
	v := f.read(0).Current
	if v == nil {
		t.Fatal("no game")
	}
	for range 2 {
		tx, err := f.db.BeginTx(f.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		finish, err := f.s.CancelUserTx(f.ctx, tx, f.users[0], f.clock.Load())
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		err = tx.Commit()
		if finish != nil {
			if err == nil {
				finish.Commit()
			} else {
				finish.Abort()
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	refunded := f.read(0).LatestResult
	if refunded.Outcome != "system_cancelled" || refunded.OwnRefund.Game != "3" || refunded.OwnRefund.General != "2" {
		t.Fatal(refunded)
	}
	f.ledger()
}
func TestGwentAIFallbackDoesNotForfeitAndPreservesChoiceWindows(t *testing.T) {
	f, bot := gwentAIFixture(t, brokenGwentAdapter{})
	deck, _ := gwentengine.PresetDeck("gemini", "standard-control")
	f.enqueueGwentAI(bot, deck)
	result := f.completeGwentAI()
	if result.Reason == "afk" {
		t.Fatal("AI fallback counted as human AFK", result)
	}
	var raw string
	if err := f.db.QueryRow("SELECT server_state_json FROM game_duel_sessions WHERE id=?", result.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Actions []duel.ActionSource `json:"actions"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		t.Fatal(raw)
	}
	fallback := 0
	for _, a := range payload.Actions {
		if a.Origin == "fallback" {
			fallback++
		}
	}
	if fallback < 3 {
		t.Fatal("multi-window fallback not exercised", fallback)
	}
	f.ledger()
}

type waitingGwentSource struct {
	ai.Source
	pending chan waitingDecision
}

func (s waitingGwentSource) Decide(ctx context.Context, r ai.Request) (ai.Result, error) {
	call := waitingDecision{r, make(chan struct{})}
	select {
	case s.pending <- call:
	case <-ctx.Done():
		return ai.Result{}, ctx.Err()
	}
	select {
	case <-call.release:
	case <-ctx.Done():
		return ai.Result{}, ctx.Err()
	}
	for _, c := range r.Actions.(ai.Choices) {
		a := c.Action.(gwentengine.Action)
		if a.Kind == "continue" {
			return ai.Selected(r, c.ID), nil
		}
	}
	return ai.Selected(r, r.Actions.(ai.Choices)[0].ID), nil
}

type waitingGwentAdapter struct {
	gwent.AIAdapter
	source waitingGwentSource
}

func (a waitingGwentAdapter) Source() ai.Source { return a.source }
func TestGwentAIDecisionSurvivesHumanMulliganAndCannotReviveDeletion(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent_mulligan", true: "deleted"}[deleting], func(t *testing.T) {
			source := waitingGwentSource{Source: gwent.AIAdapter{}.Source(), pending: make(chan waitingDecision, 8)}
			f, bot := gwentAIFixture(t, waitingGwentAdapter{source: source})
			deck, _ := gwentengine.PresetDeck("gemini", "standard-balanced")
			f.enqueueGwentAI(bot, deck)
			f.tick()
			v := f.read(0).Current
			if v == nil {
				t.Fatal("missing game")
			}
			if v.Phase == "choice" {
				var view gwentengine.View
				_ = json.Unmarshal(v.View, &view)
				body, _ := json.Marshal(view.Legal[0])
				f.action(0, *v, string(body))
				f.tick()
			}
			var call waitingDecision
			select {
			case call = <-source.pending:
			case <-time.After(time.Second):
				t.Fatal("missing AI decision")
			}
			v = f.read(0).Current
			if v == nil || v.Phase != "mulligan" {
				t.Fatal("unexpected opening", v)
			}
			if deleting {
				tx, err := f.db.BeginTx(f.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				finish, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], f.clock.Load())
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				err = tx.Commit()
				if err != nil {
					finish.Abort()
					t.Fatal(err)
				}
				finish.Commit()
			} else {
				f.action(0, *v, `{"kind":"continue"}`)
			}
			close(call.release)
			deadline := time.Now().Add(15 * time.Second)
			if deleting {
				if err := f.s.Close(); err != nil {
					t.Fatal(err)
				}
				if n := countAI(t, f, "SELECT count(*) FROM game_duel_sessions WHERE id=?", v.ID); n != 0 {
					t.Fatal("late callback revived game")
				}
				if n := countAI(t, f, "SELECT count(*) FROM game_ai_clears"); n != 0 {
					t.Fatal("late reward")
				}
			} else {
				for time.Now().Before(deadline) {
					select {
					case next := <-source.pending:
						close(next.release)
					default:
					}
					current := f.read(0).Current
					if current != nil && current.Phase == "turn" {
						return
					}
					time.Sleep(2 * time.Millisecond)
				}
				t.Fatal("human mulligan invalidated an independent AI window")
			}
		})
	}
}

func TestGwentAIChallengeEligibilitySurvivesAccountRecreation(t *testing.T) {
	f, bot := gwentAIFixture(t, passingGwentAdapter{})
	deck, _ := gwentengine.PresetDeck("openai", "standard-balanced")
	f.enqueueGwentAI(bot, deck)
	if !f.completeGwentAI().AI.FirstClear {
		t.Fatal("initial clear missing")
	}
	old := f.users[0]
	var discord string
	if err := f.db.QueryRow("SELECT discord_id FROM users WHERE id=?", old).Scan(&discord); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.s.PrepareDeleteTx(f.ctx, tx, old, f.clock.Load())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec("DELETE FROM users WHERE id=?", old); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	row, err := tx.Exec("INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,'recreated',zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),100,100)", discord)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	id, err := row.LastInsertId()
	if err != nil || id == old {
		tx.Rollback()
		t.Fatal("reused account", err)
	}
	identity, err := continuity.New(f.db, deps{})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	defer identity.Close()
	if _, err = identity.BindUserTx(f.ctx, tx, id); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		if _, err = ledger.CreateUserAssetAccount(f.ctx, tx, id, asset, f.clock.Load()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	f.users[0] = id
	if _, err = tx.Exec("INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at) VALUES(?,?,100,3700,7300,100)", f.identity(0).SessionBinding, id); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	done.Commit()
	f.clock.Add(15)
	home, err := f.s.ReadAI(f.ctx, f.identity(0))
	if err != nil || len(home.Bots) != 1 || !home.Bots[0].Completed || home.Bots[0].MemorySamples != 0 {
		t.Fatalf("recreated eligibility/memory %+v %v", home, err)
	}
	f.enqueueGwentAI(bot, deck)
	if f.completeGwentAI().AI.FirstClear {
		t.Fatal("recreated account claimed twice")
	}
	bot = f.saveBot(bot, true)
	f.clock.Add(15)
	f.enqueueGwentAI(bot, deck)
	if !f.completeGwentAI().AI.FirstClear {
		t.Fatal("explicit new challenge did not become eligible")
	}
}

func TestGwentAIDeadlineUsesLegalFallbackWithoutBotTimeoutLoss(t *testing.T) {
	source := waitingGwentSource{Source: gwent.AIAdapter{}.Source(), pending: make(chan waitingDecision, 64)}
	f, bot := gwentAIFixture(t, waitingGwentAdapter{source: source})
	deck, _ := gwentengine.PresetDeck("gemini", "standard-balanced")
	f.enqueueGwentAI(bot, deck)
	f.tick()
	id := f.read(0).Current.ID
	for range 16 {
		current := f.read(0).Current
		if current == nil {
			break
		}
		if current.Locked[current.You] {
			f.clock.Store(*current.Deadline)
			f.read(0)
		} else {
			var view gwentengine.View
			_ = json.Unmarshal(current.View, &view)
			a := view.Legal[0]
			for _, candidate := range view.Legal {
				if candidate.Kind == "continue" || candidate.Kind == "pass" {
					a = candidate
				}
			}
			raw, _ := json.Marshal(a)
			f.action(0, *current, string(raw))
		}
	}
	var timeouts int
	if err := f.db.QueryRow("SELECT timeout_count FROM game_duel_seats WHERE session_id=? AND participant_kind='bot'", id).Scan(&timeouts); err != nil || timeouts != 0 {
		t.Fatal("bot inherited human timeout accounting", timeouts, err)
	}
	var raw string
	if err := f.db.QueryRow("SELECT server_state_json FROM game_duel_sessions WHERE id=?", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Actions []duel.ActionSource `json:"actions"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		t.Fatal("invalid payload")
	}
	found := false
	for _, action := range payload.Actions {
		found = found || action.Origin == "fallback" && action.Failure == "deadline"
	}
	if !found {
		t.Fatal("deadline fallback was not exercised")
	}
	f.ledger()
}

func TestGwentAICallbackDeadlinePreservesWindowForRetry(t *testing.T) {
	source := waitingGwentSource{Source: gwent.AIAdapter{}.Source(), pending: make(chan waitingDecision, 8)}
	callbackErrors := make(chan error, 8)
	f, bot := gwentAIFixture(t, waitingGwentAdapter{source: source}, func(err error) {
		callbackErrors <- err
	})
	deck, _ := gwentengine.PresetDeck("openai", "standard-balanced")
	f.enqueueGwentAI(bot, deck)
	f.tick()
	current := f.read(0).Current
	if current == nil || current.Phase != "mulligan" {
		t.Fatal("missing opening window", current)
	}
	var first waitingDecision
	select {
	case first = <-source.pending:
	case <-time.After(5 * time.Second):
		t.Fatal("missing initial AI decision")
	}
	lock, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err := lock.ExecContext(f.ctx, `UPDATE game_duel_user_slots SET game_key=game_key WHERE 0`); err != nil {
		t.Fatal(err)
	}
	close(first.release)
	select {
	case err := <-callbackErrors:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("expected bounded callback commit to expire", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("callback did not report its commit deadline")
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n := countAI(t, f, "SELECT count(*) FROM game_duel_seats WHERE session_id=? AND participant_kind='bot' AND locked=0 AND timeout_count=0", current.ID); n != 1 {
		t.Fatal("expired callback changed the bot window or timeout count", n)
	}
	var retried waitingDecision
	deadline := time.After(5 * time.Second)
	for retried.request.DecisionID == "" {
		f.tick()
		select {
		case retried = <-source.pending:
		case <-deadline:
			t.Fatal("AI window was not retried")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if retried.request.DecisionID != first.request.DecisionID || retried.request.Window != first.request.Window {
		t.Fatal("callback deadline replaced the authoritative decision", first.request.Window, retried.request.Window)
	}
	close(retried.release)
	deadline = time.After(5 * time.Second)
	for {
		var locked bool
		if err := f.db.QueryRow("SELECT locked FROM game_duel_seats WHERE session_id=? AND participant_kind='bot'", current.ID).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		if locked {
			break
		}
		select {
		case <-deadline:
			t.Fatal("retried callback did not commit its legal choice")
		case <-time.After(10 * time.Millisecond):
		}
	}
	f.ledger()
}
