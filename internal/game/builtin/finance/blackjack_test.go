package finance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack/engine"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type blackjackFinanceFixture struct {
	t       *testing.T
	ctx     context.Context
	tx      *sql.Tx
	user    int64
	wallets ledger.AccountPair
}

func (f blackjackFinanceFixture) id(prefix string) string {
	f.t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}
func (f blackjackFinanceFixture) exec(query string, args ...any) sql.Result {
	f.t.Helper()
	r, err := f.tx.ExecContext(f.ctx, query, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func blackjackWallet(t *testing.T, database *sql.DB, total, gamePaid int64) blackjackFinanceFixture {
	t.Helper()
	f := blackjackFinanceFixture{t: t, ctx: context.Background()}
	var err error
	f.tx, err = database.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.tx.Rollback() })
	zero := db.EncodeU128(db.U128{})
	r := f.exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, f.id("bja_"), "player", zero, zero, zero, zero, zero, zero, zero, zero, 100, 100)
	f.user, err = r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		wallet, err := ledger.CreateUserAssetAccount(f.ctx, f.tx, f.user, asset, 100)
		if err != nil {
			t.Fatal(err)
		}
		external, err := ledger.CodedAssetAccount(f.ctx, f.tx, "external", asset)
		if err != nil {
			t.Fatal(err)
		}
		meta := ledger.Meta{OperationID: f.id("op_"), ActorUserID: f.user, CreatedAt: 100}
		var plan ledger.Plan
		if asset == ledger.General {
			f.wallets.General = wallet.ID
			plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(total-gamePaid), 0, ledger.Amount{}, "wallet funding")
		} else {
			f.wallets.Game = wallet.ID
			plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(gamePaid), "wallet funding")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Apply(f.ctx, f.tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f blackjackFinanceFixture) reserve(amount, gamePaid int64) (string, ports.Entry) {
	f.t.Helper()
	entryID := f.id("bjq_")
	f.exec(`INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at) VALUES(?,?,'waiting',?,100,100,100,100)`, entryID, f.user, amount)
	input := ports.Entry{Meta: ledger.Meta{OperationID: f.id("op_"), ActorUserID: f.user, CreatedAt: 100}, ResourceID: f.id("bjp_"), UserID: f.user, Amount: ledger.AmountFromMilli(amount), GamePaid: ledger.AmountFromMilli(gamePaid)}
	one, _ := db.ParseU128Decimal("1")
	err := (blackjackPort{}).Reserve(f.ctx, f.tx, input, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO game_blackjack_payments(id,entry_id,kind,hand_no,amount_milli,game_paid_milli,general_account_id,game_account_id,reserve_operation_id,state,ledger_rows_remaining,created_at) VALUES(?,?,'base',0,?,?,?,?,?,'reserved',?,100)`, input.ResourceID, entryID, amount, gamePaid, accounts.General, accounts.Game, input.Meta.OperationID, db.EncodeU128(one))
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return entryID, input
}

func (f blackjackFinanceFixture) finishMutation(input ports.Entry, state string) ports.Mutation {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE game_blackjack_payments SET state=?,ledger_rows_remaining=?,terminal_operation_id=? WHERE id=? AND state='reserved'`, state, db.EncodeU128(db.U128{}), input.Meta.OperationID, input.ResourceID)
		return err
	}
}

func financeFinalState(t *testing.T, outcome string) engine.State {
	t.Helper()
	ranks := map[string][]int{"loss": {10, 10, 9, 10}, "push": {10, 10, 8, 8}, "win": {10, 10, 10, 8}, "natural": {1, 10, 10, 8}}[outcome]
	var deck [engine.DeckSize]engine.Card
	used := [engine.DeckSize]bool{}
	for i, rank := range ranks {
		for c := range deck {
			if engine.Card(c).Rank() == rank && !used[c] {
				deck[i] = engine.Card(c)
				used[c] = true
				break
			}
		}
	}
	i := len(ranks)
	for c := range deck {
		if !used[c] {
			deck[i] = engine.Card(c)
			i++
		}
	}
	s, err := engine.Deal(deck, []int{0}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Finished {
		s, err = engine.Stop(s, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Seats[0].Hands[0].Outcome != outcome {
		t.Fatal("wrong finance fixture outcome")
	}
	return s
}

func TestBlackjackActualLedgerAllStakesAssetsAndReturns(t *testing.T) {
	file := filepath.Join(t.TempDir(), "game.db")
	dbfixture.Materialize(t, file)
	database, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for tier := int64(1); tier <= 50; tier++ {
		for _, outcome := range []string{"loss", "push", "win", "natural", "refund"} {
			for _, source := range []string{"general", "game", "mixed"} {
				t.Run(fmt.Sprintf("%d/%s/%s", tier, outcome, source), func(t *testing.T) {
					stake, gamePaid := tier*1_000_000, int64(0)
					if source == "game" {
						gamePaid = stake
					}
					if source == "mixed" {
						gamePaid = stake / 3
					}
					f := blackjackWallet(t, database, stake, gamePaid)
					entryID, input := f.reserve(stake, gamePaid)
					if err := ledger.ValidateRecovery(f.ctx, f.tx); err != nil {
						t.Fatal(err)
					}
					capacity, err := ledger.ReadCapacity(f.ctx, f.tx)
					if err != nil || capacity.ReservedFutureRows.Decimal() != "1" {
						t.Fatal("reservation missing", err)
					}
					input.Meta = ledger.Meta{OperationID: f.id("op_"), CreatedAt: 120}
					wantGeneral, wantGame := stake-gamePaid, gamePaid
					if outcome == "refund" {
						if err := (blackjackPort{}).Release(f.ctx, f.tx, input, f.finishMutation(input, "released")); err != nil {
							t.Fatal(err)
						}
					} else {
						s := financeFinalState(t, outcome)
						body, _ := json.Marshal(s)
						sessionID := f.id("bjt_")
						f.exec(`INSERT INTO game_blackjack_sessions(id,started_at,phase,revision,last_batch,state_json,view_json) VALUES(?,60,'decision',1,75,?,'{}')`, sessionID, string(body))
						f.exec(`UPDATE game_blackjack_entries SET state='seated',session_id=?,seat_no=0 WHERE id=?`, sessionID, entryID)
						f.exec(`UPDATE game_blackjack_entries SET state='playing' WHERE id=?`, entryID)
						gross := stake * int64(s.Seats[0].Hands[0].ReturnHalves()) / 2
						cut := gross / 100
						wantGeneral, wantGame = gross-3*cut, 0
						finish := ports.BlackjackSettlement{Entry: input, Net: ledger.AmountFromMilli(wantGeneral), Platform: ledger.AmountFromMilli(cut), Welfare: ledger.AmountFromMilli(cut), Thursday: ledger.AmountFromMilli(cut)}
						for kind, target := range map[string]*int64{"welfare": &finish.WelfareAccountID, "thursday": &finish.ThursdayAccountID} {
							if err := f.tx.QueryRow(`SELECT account_id FROM shared_pools WHERE pool_type=? AND state='open'`, kind).Scan(target); err != nil {
								t.Fatal(err)
							}
						}
						failure := errors.New("rollback before result")
						if err := (blackjackPort{}).Settle(f.ctx, f.tx, finish, func(context.Context, *sql.Tx) error { return failure }); !errors.Is(err, failure) {
							t.Fatal("rollback callback", err)
						}
						if after, err := ledger.ReadCapacity(f.ctx, f.tx); err != nil || after != capacity {
							t.Fatal("failed result changed capacity", err)
						}
						if err := (blackjackPort{}).Settle(f.ctx, f.tx, finish, f.finishMutation(input, "settled")); err != nil {
							t.Fatal(err)
						}
						for _, account := range []int64{finish.WelfareAccountID, finish.ThursdayAccountID} {
							a, err := ledger.ReadAccount(f.ctx, f.tx, account)
							if err != nil || a.Balance.Big().Int64() != cut {
								t.Fatal("pool cut mismatch", err)
							}
						}
					}
					for account, want := range map[int64]int64{f.wallets.General: wantGeneral, f.wallets.Game: wantGame} {
						a, err := ledger.ReadAccount(f.ctx, f.tx, account)
						if err != nil || a.Balance.Big().Int64() != want {
							t.Fatalf("wallet expected %d: %+v %v", want, a, err)
						}
					}
					if err := ledger.ValidateRecovery(f.ctx, f.tx); err != nil {
						t.Fatal(err)
					}
					if after, err := ledger.ReadCapacity(f.ctx, f.tx); err != nil || after.ReservedFutureRows.Decimal() != "0" || after.LastLedgerSeq != capacity.LastLedgerSeq+1 {
						t.Fatal("terminal capacity", err)
					}
					var ledgerKind string
					if err := f.tx.QueryRow(`SELECT kind FROM credit_operations WHERE id=? AND source_type='blackjack_payment' AND source_id=?`, input.Meta.OperationID, input.ResourceID).Scan(&ledgerKind); err != nil {
						t.Fatal(err)
					}
					if outcome == "refund" && ledgerKind != "blackjack_release" || outcome != "refund" && ledgerKind != "blackjack_settle" {
						t.Fatal("wrong ledger source")
					}
				})
			}
		}
	}
}
