package duel_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/game/ai"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding"
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/strategy"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestAIChallengeEligibilitySurvivesAccountRecreation(t *testing.T) {
	f, bot := aiFixture(t, true)
	f.aiMatch(bot.ID)
	if !f.completeAI().AI.FirstClear {
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
	f.aiMatch(bot.ID)
	if f.completeAI().AI.FirstClear {
		t.Fatal("recreated account claimed twice")
	}
	bot = f.saveBot(bot, true)
	f.clock.Add(15)
	f.aiMatch(bot.ID)
	if !f.completeAI().AI.FirstClear {
		t.Fatal("explicit new challenge did not become eligible")
	}
}

type waitingDecision struct {
	request ai.Request
	release chan struct{}
}
type waitingSource struct {
	strategy.Source
	pending chan waitingDecision
}

func (s waitingSource) Decide(ctx context.Context, r ai.Request) (ai.Result, error) {
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
	return ai.Selected(r, r.Actions.(ai.Choices)[0].ID), nil
}

type waitingAdapter struct {
	bidding.AIAdapter
	source waitingSource
}

func (a waitingAdapter) Source() ai.Source { return a.source }

func TestAICommitSurvivesOpponentLockAndCannotReviveDeletedMatch(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		name := "opponent_lock"
		if deleting {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			f, bot := aiFixture(t, false)
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			source := waitingSource{pending: make(chan waitingDecision, 4)}
			f.options.AI = waitingAdapter{source: source}
			f.options.ReportError = func(err error) { t.Logf("AI callback: %v", err) }
			var err error
			f.s, err = bidding.New(f.options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.RecoverBeforeListenAt(f.ctx, 100, 100, time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			v := f.aiMatch(bot.ID)
			var call waitingDecision
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				f.tick()
				v = *f.read(0).Current
				if v.Phase == "joker" && !v.Locked[v.You] {
					f.action(0, v, "{\"kind\":\"joker\",\"use\":false}")
				}
				select {
				case next := <-source.pending:
					if next.request.Window.Phase == "bid" {
						call = next
					} else {
						close(next.release)
					}
				default:
				}
				if call.release != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if call.release == nil {
				t.Fatal("no bid decision")
			}
			v = *f.read(0).Current
			if deleting {
				tx, err := f.db.BeginTx(f.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				done, err := f.s.PrepareDeleteTx(f.ctx, tx, f.users[0], f.clock.Load())
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				done.Commit()
			} else {
				f.action(0, v, "{\"kind\":\"bid\",\"card\":13}")
			}
			close(call.release)
			if deleting {
				if err := f.s.Close(); err != nil {
					t.Fatal(err)
				}
				var id string
				if err := f.db.QueryRow("SELECT id FROM game_duel_sessions WHERE id=?", v.ID).Scan(&id); err != sql.ErrNoRows {
					t.Fatal("deleted session survived", err)
				}
				if countAI(t, f, "SELECT count(*) FROM game_ai_memories") != 0 || countAI(t, f, "SELECT count(*) FROM game_ai_clears") != 0 {
					t.Fatal("late callback created private data")
				}
			} else {
				deadline = time.Now().Add(30 * time.Second)
				for time.Now().Before(deadline) {
					current := f.read(0).Current
					if current != nil && current.Round > v.Round {
						return
					}
					select {
					case next := <-source.pending:
						close(next.release)
					default:
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("opponent revision invalidated AI window")
			}
		})
	}
}
