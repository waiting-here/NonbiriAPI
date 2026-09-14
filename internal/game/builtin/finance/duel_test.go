package finance

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestDuelRealLedgerMatchTerminalAndRollbackAtCapacityBoundary(t *testing.T) {
	for winner := -1; winner < 2; winner++ {
		for _, edge := range []bool{false, true} {
			name := []string{"draw", "first", "second"}[winner+1]
			if edge {
				name += "/capacity"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				file := filepath.Join(t.TempDir(), "duel.db")
				dbfixture.Materialize(t, file)
				database, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)")
				if err != nil {
					t.Fatal(err)
				}
				defer database.Close()
				tx, err := database.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err := db.ApplyDuelExtension(ctx, tx); err != nil {
					t.Fatal(err)
				}
				id := func(prefix string) string {
					v, err := db.GenerateOpaqueID(prefix)
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				exec := func(query string, args ...any) sql.Result {
					r, err := tx.ExecContext(ctx, query, args...)
					if err != nil {
						t.Fatal(err)
					}
					return r
				}
				zero := db.EncodeU128(db.U128{})
				one, _ := db.ParseU128Decimal("1")
				two, _ := db.ParseU128Decimal("2")
				hash := strings.Repeat("a", 64)
				exec(`INSERT INTO game_duel_catalogs VALUES('bidding',?,1,'1',1,'{}')`, hash)
				users := [2]int64{}
				wallets := [2]ledger.AccountPair{}
				gamePaid := [2]int64{3000, 4000}
				for seat := range 2 {
					result := exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id("dah_"), "player", zero, zero, zero, zero, zero, zero, zero, zero, 100, 100)
					users[seat], err = result.LastInsertId()
					if err != nil {
						t.Fatal(err)
					}
					for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
						account, err := ledger.CreateUserAssetAccount(ctx, tx, users[seat], asset, 100)
						if err != nil {
							t.Fatal(err)
						}
						external, err := ledger.CodedAssetAccount(ctx, tx, "external", asset)
						if err != nil {
							t.Fatal(err)
						}
						amount := 5000 - gamePaid[seat]
						meta := ledger.Meta{OperationID: id("op_"), ActorUserID: users[seat], CreatedAt: 100}
						var plan ledger.Plan
						if asset == ledger.General {
							wallets[seat].General = account.ID
							plan, err = ledger.NewAdminUserAdjustment(meta, account.ID, external.ID, ledger.AmountFromMilli(amount), 0, ledger.Amount{}, "wallet funding")
						} else {
							wallets[seat].Game = account.ID
							amount = gamePaid[seat]
							plan, err = ledger.NewAdminGameAdjustment(meta, account.ID, external.ID, ledger.AmountFromMilli(amount), "wallet funding")
						}
						if err != nil {
							t.Fatal(err)
						}
						if _, err := ledger.Apply(ctx, tx, plan); err != nil {
							t.Fatal(err)
						}
					}
				}
				port := duelPort{game: "bidding"}
				queues := [2]ports.QueueInput{}
				for seat, user := range users {
					qid := id("bidq_")
					meta := ledger.Meta{OperationID: id("op_"), ActorUserID: user, CreatedAt: 100}
					entry := ports.Entry{Meta: meta, ResourceID: qid, UserID: user, Amount: ledger.AmountFromMilli(5000), GamePaid: ledger.AmountFromMilli(gamePaid[seat])}
					err := port.QueueReserve(ctx, tx, entry, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
						_, err := tx.ExecContext(ctx, `INSERT INTO game_duel_queue(id,game_key,mode,user_id,revision,created_at,deadline,terms_json,terms_hash,content_hash,ticket_milli,game_paid_milli,reservation_operation_id,general_account_id,game_account_id,ledger_rows_remaining,device_hash,ip_hash) VALUES(?,'bidding','tier1',?,?,100,220,'{}',?,?,5000,?,?,?,?,?,zeroblob(32),zeroblob(32))`, qid, user, db.EncodeU128(one), hash, hash, gamePaid[seat], meta.OperationID, accounts.General, accounts.Game, db.EncodeU128(one))
						if err != nil {
							return err
						}
						_, err = tx.ExecContext(ctx, `INSERT INTO game_duel_user_slots(user_id,game_key,queue_id) VALUES(?,'bidding',?)`, user, qid)
						return err
					})
					if err != nil {
						t.Fatal(err)
					}
					queues[seat] = ports.QueueInput{QueueID: qid, UserID: user, Amount: entry.Amount, GamePaid: entry.GamePaid}
				}
				capacity, err := ledger.ReadCapacity(ctx, tx)
				if err != nil || capacity.ReservedFutureRows.Decimal() != "2" {
					t.Fatalf("queue capacity %+v %v", capacity, err)
				}
				lastBefore := capacity.LastLedgerSeq
				// A synthetic offset exercises the final two operation numbers. The
				// normal case separately verifies contiguous full ledger recovery.
				if edge {
					lastBefore = math.MaxInt64 - 2
					exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, lastBefore)
				}
				sid := id("bid_")
				err = port.SessionStart(ctx, tx, ports.DuelStart{Meta: ledger.Meta{OperationID: id("op_"), CreatedAt: 101}, SessionID: sid, Queues: queues}, func(ctx context.Context, tx *sql.Tx, accounts ledger.AccountPair) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO game_duel_sessions(id,game_key,mode,content_hash,terms_json,terms_hash,ticket_milli,platform_bp,welfare_bp,thursday_bp,state,phase,round,revision,phase_seq,started_at,phase_deadline,general_account_id,game_account_id,ledger_rows_remaining,server_state_json,initial_state_json) VALUES(?,'bidding','tier1',?,'{}',?,5000,3333,3333,3333,'active','bid',1,?,?,101,121,?,?,?,'{}','{}')`, sid, hash, hash, db.EncodeU128(one), db.EncodeU128(one), accounts.General, accounts.Game, db.EncodeU128(one))
					if err != nil {
						return err
					}
					for seat, q := range queues {
						if _, err := tx.ExecContext(ctx, `INSERT INTO game_duel_seats(session_id,seat_no,user_id,general_paid_milli,game_paid_milli,locked,timeout_count) VALUES(?,?,?,?,?,0,0)`, sid, seat, q.UserID, 5000-gamePaid[seat], gamePaid[seat]); err != nil {
							return err
						}
						if _, err := tx.ExecContext(ctx, `UPDATE game_duel_user_slots SET queue_id=NULL,session_id=? WHERE user_id=? AND game_key='bidding'`, sid, q.UserID); err != nil {
							return err
						}
						if _, err := tx.ExecContext(ctx, `UPDATE game_duel_queue SET ledger_rows_remaining=? WHERE id=?`, zero, q.QueueID); err != nil {
							return err
						}
						if _, err := tx.ExecContext(ctx, `DELETE FROM game_duel_queue WHERE id=?`, q.QueueID); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				capacity, err = ledger.ReadCapacity(ctx, tx)
				if err != nil || capacity.LastLedgerSeq != lastBefore+1 || capacity.ReservedFutureRows.Decimal() != "1" {
					t.Fatalf("match requested extra capacity: %+v %v", capacity, err)
				}
				if err := func() error {
					if edge {
						return db.ValidateAssetBalances(ctx, tx)
					}
					return ledger.ValidateRecovery(ctx, tx)
				}(); err != nil {
					t.Fatal(err)
				}
				var winning *int
				var winnerSQL any
				outcome, reason := "draw", "rounds"
				if winner >= 0 {
					winning = &winner
					winnerSQL = winner
					outcome, reason = "decided", "surrender"
				}
				input := ports.DuelFinish{Meta: ledger.Meta{OperationID: id("op_"), CreatedAt: 102}, SessionID: sid, Winner: winning}
				for kind, target := range map[string]*int64{"welfare": &input.WelfareAccountID, "thursday": &input.ThursdayAccountID} {
					if err := tx.QueryRowContext(ctx, `SELECT account_id FROM shared_pools WHERE pool_type=? AND state='open'`, kind).Scan(target); err != nil {
						t.Fatal(err)
					}
				}
				failure := errors.New("stop before terminal")
				if err := port.Terminal(ctx, tx, input, func(context.Context, *sql.Tx, ledger.DuelCuts) error { return failure }); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				if after, err := ledger.ReadCapacity(ctx, tx); err != nil || after != capacity {
					t.Fatal("failed settlement changed capacity")
				}
				err = port.Terminal(ctx, tx, input, func(ctx context.Context, tx *sql.Tx, cuts ledger.DuelCuts) error {
					_, err := tx.ExecContext(ctx, `UPDATE game_duel_sessions SET state='terminal',phase='terminal',revision=?,phase_seq=?,phase_deadline=NULL,ledger_rows_remaining=?,terminal_at=102,delete_at=2592102,outcome=?,reason=?,winner_seat=?,score0=0,score1=0,prize_milli=?,platform_milli=?,welfare_milli=?,thursday_milli=?,terminal_operation_id=? WHERE id=?`, db.EncodeU128(two), db.EncodeU128(two), zero, outcome, reason, winnerSQL, cuts.Prize.Big().Int64(), cuts.Platform.Big().Int64(), cuts.Welfare.Big().Int64(), cuts.Thursday.Big().Int64(), input.Meta.OperationID, sid)
					if err != nil {
						return err
					}
					_, err = tx.ExecContext(ctx, `DELETE FROM game_duel_user_slots WHERE session_id=?`, sid)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				capacity, err = ledger.ReadCapacity(ctx, tx)
				if err != nil || capacity.LastLedgerSeq != lastBefore+2 || capacity.ReservedFutureRows.Decimal() != "0" {
					t.Fatal("terminal did not consume final reserved row")
				}
				for seat, pair := range wallets {
					wantGeneral, wantGame := int64(0), int64(0)
					if winner == -1 || winner == seat {
						wantGeneral = 5000 - gamePaid[seat]
						wantGame = gamePaid[seat]
					}
					if winner == seat {
						wantGeneral += 2
					}
					for account, want := range map[int64]int64{pair.General: wantGeneral, pair.Game: wantGame} {
						got, err := ledger.ReadAccount(ctx, tx, account)
						if err != nil || got.Balance.Big().Int64() != want {
							t.Fatalf("wallet %d want %d got %+v %v", account, want, got, err)
						}
					}
				}
				for _, account := range []int64{input.WelfareAccountID, input.ThursdayAccountID} {
					want := int64(0)
					if winner >= 0 {
						want = 1666
					}
					got, err := ledger.ReadAccount(ctx, tx, account)
					if err != nil || got.Balance.Big().Int64() != want {
						t.Fatal("incorrect independently rounded pool cut", got, err)
					}
				}
				if err := func() error {
					if edge {
						return db.ValidateAssetBalances(ctx, tx)
					}
					return ledger.ValidateRecovery(ctx, tx)
				}(); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
