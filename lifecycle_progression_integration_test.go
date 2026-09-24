package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/ranking"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func readPersonalExport(t *testing.T, f *duelWireFixture, seat int) lifecycle.ExportDocument {
	t.Helper()
	r := f.call(seat, "POST", "/api/account/export", nil, true)
	var document lifecycle.ExportDocument
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &document) != nil || document.SchemaVersion != 10 || r.Header().Get("Content-Disposition") != `attachment; filename="nonbiriapi-account-export-v10.json"` {
		t.Fatal("invalid personal export", r.Code, r.Body.String())
	}
	return document
}

func advancePersonalRankingFixture(t *testing.T, f *duelWireFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for ready := false; !ready; {
		tx, err := f.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		ready, err = ranking.AdvanceTx(ctx, tx, f.clock.Load())
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExportConvergesGameFactsAndBalancesAtomically(t *testing.T) {
	f := newDuelWireFixture(t)
	f.admin("PATCH", "/admin/api/games/config", map[string]any{"expected_revision": "3", "linklink": map[string]any{"enabled": true, "specs": map[string]any{"6x8": map[string]any{"enabled": true, "price": "10"}}}}, 200)
	started := f.call(0, "POST", "/api/games/linklink/sessions", map[string]string{"spec": "6x8"}, false)
	var state linklink.State
	if started.Code != 201 || json.Unmarshal(started.Body.Bytes(), &state) != nil {
		t.Fatal("start LinkLink", started.Code)
	}
	f.clock.Store(state.Deadline)
	if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_export_rank BEFORE INSERT ON game_rank_events BEGIN SELECT RAISE(ABORT,'export fixture'); END`); err != nil {
		t.Fatal(err)
	}
	r := f.call(0, "POST", "/api/account/export", nil, true)
	if r.Code < 500 {
		t.Fatal("failed settlement returned a partial export", r.Code)
	}
	var active int
	var completions int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM game_linklink_sessions WHERE id=?`, state.SessionID).Scan(&active); err != nil || active != 1 {
		t.Fatal("failed export committed game state", active, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM game_onboarding_completions`).Scan(&completions); err != nil || completions != 0 {
		t.Fatal("failed export committed rewards", completions, err)
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER fail_export_rank`); err != nil {
		t.Fatal(err)
	}
	document := readPersonalExport(t, f, 0)
	if len(document.LinkLink.Summaries) != 1 || len(document.GameOnboarding) != 0 || len(document.GameOnboardingHolds) != 0 || len(document.GameRankings.Events) != 1 {
		t.Fatal("export omitted settled game facts", len(document.LinkLink.Summaries), len(document.GameOnboarding), len(document.GameOnboardingHolds), len(document.GameRankings.Events))
	}
	for asset, balance := range map[string]string{"general": document.User.Balance, "game": document.User.GameBalance} {
		total := new(big.Rat)
		for _, row := range document.CreditLedger {
			if row.Asset == asset {
				delta, ok := new(big.Rat).SetString(row.Delta)
				if !ok {
					t.Fatal("invalid exported amount")
				}
				total.Add(total, delta)
			}
		}
		wallet, ok := new(big.Rat).SetString(balance)
		if !ok || wallet.Cmp(total) != 0 {
			t.Fatal("wallet preceded lazy settlement", asset, balance, total)
		}
	}
	f.checkLedger()
}

func TestPersonalHistoryExportExpiryOwnershipAndDeletion(t *testing.T) {
	f := newDuelWireFixture(t)
	ctx := context.Background()
	for key, value := range map[string]string{"charity_enabled": "1", antiabuse.KeyCharityViolationDeductMilli: "1000", antiabuse.KeyCharityViolationBanSeconds: "0"} {
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, value, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.app.forward.abuse.RecordShort(ctx, f.users[0], "[公益]fixture/model", 1); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(time.Now().Unix())
	now := f.clock.Load()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, user := range f.users {
		if err := ranking.RecordTx(ctx, tx, ranking.Contribution{UserID: user, Game: "bidding", Source: "history-fixture", SettledAt: now, Loss: big.NewInt(1000), PositiveProfit: big.NewInt(2000)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	document := readPersonalExport(t, f, 0)
	if len(document.Penalties) != 1 || len(document.Penalties[0].Actions) != 1 || document.Penalties[0].Actions[0].RequestID == nil || len(document.GameRankings.Events) != 1 {
		t.Fatal("missing personal history")
	}
	penalties, _ := json.Marshal(document.Penalties)
	for _, field := range []string{"rules", "statistics", "count", "evidence", "actor_user_id", "threshold"} {
		if strings.Contains(string(penalties), `"`+field+`"`) {
			t.Fatal("unsafe penalty projection", field)
		}
	}
	if len(readPersonalExport(t, f, 1).Penalties) != 0 {
		t.Fatal("penalty crossed owner boundary")
	}
	f.clock.Store(now + 7*86400)
	// Expiry projection requires completed bounded maintenance. Under race
	// instrumentation even these small groups may need multiple committed passes.
	advancePersonalRankingFixture(t, f)
	document = readPersonalExport(t, f, 0)
	if len(document.GameRankings.Events) != 1 || document.GameRankings.Events[0].Loss != nil || document.GameRankings.Events[0].PositiveProfit == nil {
		t.Fatal("expired charity payload survived export")
	}
	f.clock.Store(now + 31*86400)
	advancePersonalRankingFixture(t, f)
	document = readPersonalExport(t, f, 0)
	if len(document.GameRankings.Events) != 0 || len(document.Penalties) != 1 || document.Penalties[0].Actions[0].RequestID != nil {
		t.Fatal("request and contribution retention differed")
	}
	f.clock.Store(now + 90*86400)
	if len(readPersonalExport(t, f, 0).Penalties) != 0 {
		t.Fatal("expired penalty exported")
	}
	r := f.call(0, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
	if r.Code != 204 {
		t.Fatal("delete personal history", r.Code)
	}
	for _, table := range []string{"game_rank_events", "game_rank_totals", "abuse_cases", "abuse_windows"} {
		var count int
		if err := f.store.DB().QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, f.users[0]).Scan(&count); err != nil || count != 0 {
			t.Fatal("deleted personal history survived", table, count, err)
		}
	}
	f.checkLedger()
}

func TestPersonalExportRankingBudgetRollsBackAndRetries(t *testing.T) {
	f := newDuelWireFixture(t)
	// This assertion isolates export rollback from independently committed
	// maintenance progress, which is covered by the ranking worker tests.
	f.app.rankingCancel()
	<-f.app.rankingDone
	// Cold database bootstrap establishes the statistics epoch after the
	// fixture's initial clock. Seed contributions at a current decision time.
	f.clock.Store(time.Now().Unix())
	ctx := context.Background()
	now := f.clock.Load()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range 513 {
		if err := ranking.RecordTx(ctx, tx, ranking.Contribution{UserID: f.users[0], Game: "fishing", Source: fmt.Sprintf("budget-%d", i), SettledAt: now, Loss: big.NewInt(1000), PositiveProfit: new(big.Int)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seeded int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM game_rank_events`).Scan(&seeded); err != nil || seeded != 513 {
		t.Fatal("incomplete ranking backlog fixture", seeded, err)
	}
	f.clock.Store(now + 7*86400)
	r := f.call(0, "POST", "/api/account/export", nil, true)
	if r.Code != 503 || r.Header().Get("Content-Disposition") != "" {
		t.Fatal("catch-up returned a partial download", r.Code)
	}
	var remaining, scratch int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM game_rank_events`).Scan(&remaining); err != nil || remaining != 513 {
		t.Fatal("failed export advanced facts", remaining, err)
	}
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM game_rank_expiry_work`).Scan(&scratch); err != nil || scratch != 0 {
		t.Fatal("failed export retained scratch", scratch, err)
	}
	advancePersonalRankingFixture(t, f)
	if len(readPersonalExport(t, f, 0).GameRankings.Events) != 0 {
		t.Fatal("retry exported expired facts")
	}
}

func TestPersonalExportDonationAchievementUsesLedgerFact(t *testing.T) {
	f := newDuelWireFixture(t)
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.UserAssetAccount(ctx, tx, f.users[0], ledger.General)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: id, ActorUserID: f.adminID, CreatedAt: f.clock.Load()}, wallet.ID, external.ID, ledger.Amount{}, f.users[0], ledger.AmountFromMilli(1234), "synthetic achievement")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	value := readPersonalExport(t, f, 0).User
	if value.DonationCreditAchievedAt == nil || *value.DonationCreditAchievedAt != f.clock.Load() || value.DonationCredit != "1.234" {
		t.Fatal("donation achievement missing", value.DonationCredit)
	}
	if readPersonalExport(t, f, 1).User.DonationCreditAchievedAt != nil {
		t.Fatal("achievement crossed owner boundary")
	}
	f.checkLedger()
}
