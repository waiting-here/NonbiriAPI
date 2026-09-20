package finance

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func rewardFinalState(t *testing.T, ranks []int, split, hit bool) engine.State {
	t.Helper()
	var deck [engine.DeckSize]engine.Card
	used := map[engine.Card]bool{}
	for i, rank := range ranks {
		for c := range deck {
			card := engine.Card(c)
			if card.Rank() == rank && !used[card] {
				deck[i], used[card] = card, true
				break
			}
		}
	}
	i := len(ranks)
	for c := range deck {
		card := engine.Card(c)
		if !used[card] {
			deck[i] = card
			i++
		}
	}
	s, err := engine.Deal(deck, []int{0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if split {
		s, err = engine.ApplyBatch(s, []engine.Action{{Seat: 0, Hand: 0, Revision: s.Seats[0].Hands[0].Revision, Kind: "split"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if hit {
		hand := 0
		if split {
			hand = 1
		}
		s, err = engine.ApplyBatch(s, []engine.Action{{Seat: 0, Hand: hand, Revision: s.Seats[0].Hands[hand].Revision, Kind: "hit"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !s.Finished {
		s, err = engine.Stop(s, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBlackjackMultipleAwardsUseHandsAndAtomicLedger(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ranks         []int
		split, hit    bool
		gross, reward int64
		keys          []string
	}{
		{"natural_win", []int{1, 10, 10, 8}, false, false, 2500000, 12000000, []string{"complete", "first_21", "first_natural_21", "first_win"}},
		{"natural_tie", []int{1, 1, 10, 10}, false, false, 1000000, 10000000, []string{"complete", "first_21", "first_natural_21"}},
		{"split_win_and_bust_with_net_loss", []int{10, 10, 10, 9, 1, 7, 10}, true, true, 2000000, 10000000, []string{"complete", "first_21", "first_bust", "first_win"}},
		{"all_bust", []int{10, 10, 6, 9, 10}, false, true, 0, 4000000, []string{"complete", "first_bust"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "game.db")
			dbfixture.Materialize(t, file)
			database, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { database.Close() })
			total := int64(1000000)
			if tc.split {
				total *= 2
			}
			f := blackjackWallet(t, database, total, 0)
			entry, input := f.reserve(1000000, 0)
			var epoch int64
			if err := f.tx.QueryRow(`SELECT started_at FROM game_statistics_epoch`).Scan(&epoch); err != nil {
				t.Fatal(err)
			}
			start := (epoch/60 + 1) * 60
			p := blackjackPort{}
			if n, more, err := p.RestoreOnboarding(f.ctx, f.tx, 1, start); err != nil || n != 1 || !more {
				t.Fatal("restore", n, more, err)
			}
			if n, more, err := p.RestoreOnboarding(f.ctx, f.tx, 1, start); err != nil || n != 0 || more {
				t.Fatal("restore replay", n, more, err)
			}
			var holds int
			if err := f.tx.QueryRow(`SELECT count(*) FROM game_onboarding_holds WHERE blackjack_entry_id=?`, entry).Scan(&holds); err != nil || holds != 5 {
				t.Fatal(holds, err)
			}
			state := rewardFinalState(t, tc.ranks, tc.split, tc.hit)
			body, _ := json.Marshal(state)
			session := f.id("bjt_")
			f.exec(`INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch,state_json,view_json) VALUES(?,?,'decision',1,?,?,'{}')`, session, start, start+15, string(body))
			f.exec(`UPDATE game_blackjack_entries SET state='seated',session_id=?,seat_no=0 WHERE id=?`, session, entry)
			f.exec(`UPDATE game_blackjack_entries SET state='playing' WHERE id=?`, entry)
			inputs := []ports.Entry{input}
			if tc.split {
				extra := ports.Entry{Meta: ledger.Meta{OperationID: f.id("op_"), ActorUserID: f.user, CreatedAt: start + 16}, ResourceID: f.id("bjp_"), UserID: f.user, Amount: ledger.AmountFromMilli(1000000)}
				one, _ := db.ParseU128Decimal("1")
				if err := p.Reserve(f.ctx, f.tx, extra, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_payments(id,entry_id,kind,hand_no,amount_milli,game_paid_milli,general_account_id,game_account_id,reserve_operation_id,state,ledger_rows_remaining,created_at) VALUES(?,?,'split',1,1000000,0,?,?,?,'reserved',?,?)`, extra.ResourceID, entry, accounts.General, accounts.Game, extra.Meta.OperationID, db.EncodeU128(one), start+16)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				inputs = append(inputs, extra)
			}
			before, err := ledger.ReadCapacity(f.ctx, f.tx)
			if err != nil {
				t.Fatal(err)
			}
			for i, in := range inputs {
				in.Meta = ledger.Meta{OperationID: f.id("op_"), CreatedAt: start + 45}
				finish := ports.BlackjackSettlement{Entry: in}
				if i == 0 {
					finish.Net = ledger.AmountFromMilli(tc.gross - tc.gross/100*3)
					finish.Platform, finish.Welfare, finish.Thursday = ledger.AmountFromMilli(tc.gross/100), ledger.AmountFromMilli(tc.gross/100), ledger.AmountFromMilli(tc.gross/100)
					for kind, target := range map[string]*int64{"welfare": &finish.WelfareAccountID, "thursday": &finish.ThursdayAccountID} {
						if err := f.tx.QueryRow(`SELECT account_id FROM shared_pools WHERE pool_type=? AND state='open'`, kind).Scan(target); err != nil {
							t.Fatal(err)
						}
					}
					f.exec(`SAVEPOINT reward_failure`)
					f.exec(`CREATE TEMP TRIGGER fail_reward BEFORE INSERT ON game_onboarding_completions WHEN NEW.task_key<>'complete' BEGIN SELECT RAISE(ABORT,'reward failure'); END`)
					if err := p.Settle(f.ctx, f.tx, finish, f.finishMutation(in, "settled")); err == nil {
						t.Fatal("injected failure succeeded")
					}
					f.exec(`ROLLBACK TO reward_failure`)
					f.exec(`RELEASE reward_failure`)
					if after, err := ledger.ReadCapacity(f.ctx, f.tx); err != nil || before != after {
						t.Fatal("capacity changed after rollback", after, err)
					}
					var count int
					if err := f.tx.QueryRow(`SELECT count(*) FROM game_onboarding_completions`).Scan(&count); err != nil || count != 0 {
						t.Fatal("partial reward", count, err)
					}
				}
				if err := p.Settle(f.ctx, f.tx, finish, f.finishMutation(in, "settled")); err != nil {
					t.Fatal(err)
				}
				if err := p.Settle(f.ctx, f.tx, finish, f.finishMutation(in, "settled")); err == nil {
					t.Fatal("repeated payment accepted")
				}
			}
			rows, err := f.tx.Query(`SELECT task_key FROM game_onboarding_completions WHERE user_id=? ORDER BY task_key`, f.user)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for rows.Next() {
				var key string
				if err := rows.Scan(&key); err != nil {
					t.Fatal(err)
				}
				got = append(got, key)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.keys) {
				t.Fatal(got, tc.keys)
			}
			wallet, err := ledger.ReadAccount(f.ctx, f.tx, f.wallets.General)
			if err != nil || wallet.Balance.Big().Int64() != tc.gross-tc.gross/100*3+tc.reward {
				t.Fatal(wallet, err)
			}
			var lossSign int
			var lossMag, profitRaw []byte
			if err := f.tx.QueryRow(`SELECT loss_sign,loss_mag,positive_profit FROM game_rank_events WHERE user_id=? AND game_key='blackjack' AND source_id=?`, f.user, entry).Scan(&lossSign, &lossMag, &profitRaw); err != nil {
				t.Fatal(err)
			}
			loss, err := db.NewSM128(lossSign, lossMag)
			if err != nil {
				t.Fatal(err)
			}
			profit, err := db.DecodeU128(profitRaw)
			if err != nil {
				t.Fatal(err)
			}
			wantProfit := int64(0)
			if tc.name == "natural_win" {
				wantProfit = 1500000
			}
			if tc.split {
				wantProfit = 1000000
			}
			if loss.Big().Int64() != total-tc.gross+tc.gross/100*3 || profit.Big().Int64() != wantProfit {
				t.Fatal("hand ranking includes rewards or offsets a winning hand", loss, profit, wantProfit)
			}
			if after, err := ledger.ReadCapacity(f.ctx, f.tx); err != nil || after.ReservedFutureRows.Decimal() != "0" {
				t.Fatal(after, err)
			}
			if err := ledger.ValidateRecovery(f.ctx, f.tx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
