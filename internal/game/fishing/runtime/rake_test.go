package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func fixedRakeCatches(count int) *scriptedSource {
	values := make([]uint64, 0, count*4)
	for range count {
		values = append(values, 4728, 0, 37)
	}
	for range count {
		values = append(values, 1)
	}
	return &scriptedSource{values: values}
}

func TestFishingV2NetPayoutLedgerAndPerOutcomeRounding(t *testing.T) {
	for _, count := range []int{1, 10} {
		t.Run(game.FormatAmount(int64(count)), func(t *testing.T) {
			f := newGameFixture(t, fixedRakeCatches(count))
			ctx := context.Background()
			user := f.seedUser("net-catch", fixtureFunding)
			tx, err := f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			wallet, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
			if err != nil {
				t.Fatal(err)
			}
			external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: f.mustID("op_"), ActorUserID: f.adminID, CreatedAt: fixtureNow}, wallet.ID, external.ID, ledger.AmountFromMilli(1_000_000), "fund game wallet")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ledger.Apply(ctx, tx, plan); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			input := StartInput{UserID: user, Bait: "worm", Count: count, IdempotencyKey: validTestKey(9971)}
			body, err := json.Marshal(startBody{Bait: input.Bait, Count: input.Count})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "http://example.test"+RouteFishingBatches, bytes.NewReader(body))
			request.Header.Set("Idempotency-Key", input.IdempotencyKey)
			response := httptest.NewRecorder()
			(&httpAPI{service: f.service}).start(response, request, resources.UserPrincipal{UserID: user})
			if response.Code != http.StatusOK {
				t.Fatalf("HTTP start: %d %s", response.Code, response.Body.String())
			}
			var result FishingBatchResult
			decoder := json.NewDecoder(response.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&result); err != nil {
				t.Fatal(err)
			}
			gross, cut, net := int64(52_640_479), int64(526_404), int64(51_061_267)
			if result.RulesVersion != 2 || result.NetPayoutTotal != game.FormatAmount(net*int64(count)) ||
				result.PayoutTotal != game.FormatAmount(gross*int64(count)) || result.Rake != (FishingRake{game.FormatAmount(cut * int64(count)), game.FormatAmount(cut * int64(count)), game.FormatAmount(cut * int64(count))}) {
				t.Fatalf("frozen result=%+v", result)
			}
			for _, outcome := range result.Outcomes {
				if outcome.Reward != game.FormatAmount(gross) || outcome.NetReward != game.FormatAmount(net) || outcome.Rake != rakeFromMilli(cut, cut, cut) {
					t.Fatalf("outcome=%+v", outcome)
				}
			}
			if count == 10 && cut*10 == gross*10/100 {
				t.Fatal("fixture must distinguish per-outcome and batch rounding")
			}
			generalFee := 2_500_000*int64(count) - 1_000_000
			if result.Payment != (game.Payment{General: game.FormatAmount(generalFee), Game: "1000"}) || result.GameBalance != "0" ||
				result.Balance != game.FormatAmount(fixtureFunding-generalFee+net*int64(count)+1_000_000) {
				t.Fatalf("wallet/payment=%+v", result)
			}
			tx, err = f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			platform, err := ledger.CodedAccount(ctx, tx, "platform")
			if err != nil {
				t.Fatal(err)
			}
			gamePlatform, err := ledger.CodedAssetAccount(ctx, tx, "platform", ledger.Game)
			if err != nil {
				t.Fatal(err)
			}
			if platform.Balance.Big().Int64() != generalFee+cut*int64(count) || gamePlatform.Balance.Big().Int64() != 1_000_000 {
				t.Fatal("ticket/rake platform accounts")
			}
			for _, kind := range []string{"welfare", "thursday"} {
				var accountID, revision int64
				if err = tx.QueryRowContext(ctx, `SELECT account_id,revision FROM shared_pools WHERE pool_type=? AND state='open'`, kind).Scan(&accountID, &revision); err != nil {
					t.Fatal(err)
				}
				account, err := ledger.ReadAccount(ctx, tx, accountID)
				if err != nil {
					t.Fatal(err)
				}
				if account.Balance.Big().Int64() != cut*int64(count) || revision != 2 {
					t.Fatalf("%s balance=%s revision=%d", kind, account.Balance.Big(), revision)
				}
			}
			var rankRaw []byte
			if err = tx.QueryRowContext(ctx, `SELECT total_payout FROM game_fishing_rank_aggregates WHERE user_id=?`, user).Scan(&rankRaw); err != nil {
				t.Fatal(err)
			}
			rank, err := db.DecodeU128(rankRaw)
			if err != nil || rank.Big().Cmp(big.NewInt(net*int64(count))) != 0 {
				t.Fatal("ranking includes gross or newcomer reward")
			}
			if err = ledger.ValidateRecovery(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			events := f.service.activityEvents.(*testPoolEvents)
			if events.count.Load() != 1 {
				t.Fatal("missing committed pool event")
			}
			replay, _, err := f.service.StartFishing(ctx, input)
			if err != nil || replay == nil || !replay.IdempotentReplay || replay.Rake != result.Rake || events.count.Load() != 1 {
				t.Fatal("replay duplicated pool transfer", err)
			}
			tx, err = f.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			export, err := f.service.Lifecycle().ExportTx(ctx, tx, user, fixtureNow, 100)
			if err != nil || len(export.Terminal) != 1 || export.Terminal[0].NetPayoutTotal != result.NetPayoutTotal || export.Terminal[0].Rake != result.Rake {
				t.Fatalf("export=%+v err=%v", export, err)
			}
			_ = tx.Rollback()
		})
	}
}

func TestFishingV2FrozenRakeRollsBackWithPoolFailure(t *testing.T) {
	f := newGameFixture(t, fixedRakeCatches(1))
	ctx := context.Background()
	user := f.seedUser("retry-rake", fixtureFunding)
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := f.service.StartFishing(ctx, StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(9972)})
	if err != nil || pending == nil {
		t.Fatal("reserve", err)
	}
	if _, err = f.database.Exec(`UPDATE site_config SET value=CASE WHEN key='game_fishing_rtp' THEN '100' ELSE '0' END WHERE key IN ('game_fishing_rtp','game_fishing_rake_platform_bp','game_fishing_rake_welfare_bp','game_fishing_rake_thursday_bp')`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec(`CREATE TRIGGER fail_pool_revision BEFORE UPDATE OF revision ON shared_pools BEGIN SELECT RAISE(ABORT,'pool revision failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.service.beforeSettlement = nil
	if _, err = f.service.settle(ctx, pending.BatchID, user, fixtureNow, false); err == nil {
		t.Fatal("injected settlement succeeded")
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM game_onboarding_completions",
		"SELECT COUNT(*) FROM credit_operations WHERE kind IN ('fishing_settle','game_onboarding_reward')",
		"SELECT COUNT(*) FROM game_fishing_rank_facts",
	} {
		if f.scalar(query) != 0 {
			t.Fatalf("partial terminal commit: %s", query)
		}
	}
	if f.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 1 || f.scalar("SELECT COUNT(*) FROM game_fishing_batches WHERE state='reserved'") != 1 {
		t.Fatal("lost accepted capacity")
	}
	if f.service.activityEvents.(*testPoolEvents).count.Load() != 0 {
		t.Fatal("uncommitted notification")
	}
	if _, err = f.database.Exec("DROP TRIGGER fail_pool_revision"); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.settle(ctx, pending.BatchID, user, fixtureNow, false)
	if err != nil || result == nil || result.NetPayoutTotal != "51061.267" || result.Rake != rakeFromMilli(526404, 526404, 526404) {
		t.Fatalf("configuration repriced accepted catch: %+v %v", result, err)
	}
	if f.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 1 {
		t.Fatal("reward after retry")
	}
	if err = f.service.AcknowledgeFishing(ctx, user, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = ledger.ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
}
