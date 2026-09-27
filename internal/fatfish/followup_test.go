package fatfish

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestCapacityReclaimsExpiredTerminalSummariesBeforeAdmission(t *testing.T) {
	zero := Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}}
	t.Run("user prepare", func(t *testing.T) {
		f := newFishFixture(t)
		period, node := f.publishWithAmounts(t, zero)
		ctx := context.Background()
		var oldID string
		if err := f.db.QueryRow(`SELECT id FROM fatfish_challenges WHERE playtest=1 AND terminal_at_ms IS NOT NULL`).Scan(&oldID); err != nil {
			t.Fatal(err)
		}
		f.tick(int64(31 * 24 * time.Hour / time.Millisecond))
		end := f.clock.Load()/1000 + 3600
		if _, err := f.db.Exec(`UPDATE limited_activity_configs SET ends_at=? WHERE activity_key='fat-fish'`, end); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`UPDATE fatfish_periods SET ends_at=?,revision=revision+1 WHERE id=?`, end, period); err != nil {
			t.Fatal(err)
		}
		p, err := f.s.AdminPeriod(ctx, f.admin, period)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(3001)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`UPDATE fatfish_capacity SET summary_rows=1000000 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		_, hash := fishCap(201)
		prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(3002))
		if err != nil {
			t.Fatalf("old summary should make one slot: %v", err)
		}
		var oldCount, currentCount, summaryCount int
		if err := f.db.QueryRow(`SELECT count(*) FROM fatfish_challenges WHERE id=?`, oldID).Scan(&oldCount); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`SELECT count(*) FROM fatfish_challenges WHERE id=?`, prepared.ID).Scan(&currentCount); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`SELECT summary_rows FROM fatfish_capacity WHERE id=1`).Scan(&summaryCount); err != nil {
			t.Fatal(err)
		}
		if oldCount != 0 || currentCount != 1 || summaryCount != 1000000 {
			t.Fatalf("counter decision old=%d current=%d counter=%d", oldCount, currentCount, summaryCount)
		}
		_, hash = fishCap(202)
		if _, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: p.Nodes[0].VersionID, TabCapabilityHash: hash}, fishKey(3003)); !errors.Is(err, ErrCapacity) {
			t.Fatalf("non-expired prepared row must not be pruned: %v", err)
		}
		if err := f.db.QueryRow(`SELECT count(*) FROM fatfish_challenges WHERE id=?`, prepared.ID).Scan(&currentCount); err != nil || currentCount != 1 {
			t.Fatalf("live row changed at capacity: %d %v", currentCount, err)
		}
	})
	t.Run("playtest exact retention boundary", func(t *testing.T) {
		f := newFishFixture(t)
		period, _ := f.publishWithAmounts(t, zero)
		ctx := context.Background()
		p, err := f.s.AdminPeriod(ctx, f.admin, period)
		if err != nil {
			t.Fatal(err)
		}
		var oldTerminal int64
		if err := f.db.QueryRow(`SELECT terminal_at_ms FROM fatfish_challenges WHERE playtest=1 AND terminal_at_ms IS NOT NULL`).Scan(&oldTerminal); err != nil {
			t.Fatal(err)
		}
		f.tick(oldTerminal + int64(summaryLifetime/time.Millisecond) - f.clock.Load())
		if _, err := f.db.Exec(`UPDATE fatfish_capacity SET summary_rows=1000000 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		_, hash := fishCap(203)
		if _, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: p.Nodes[0].VersionID, TabCapabilityHash: hash}, fishKey(3004)); !errors.Is(err, ErrCapacity) {
			t.Fatalf("exact 30-day summary remains: %v", err)
		}
		f.tick(1)
		prepared, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: p.Nodes[0].VersionID, TabCapabilityHash: hash}, fishKey(3005))
		if err != nil || prepared.State != "prepared" {
			t.Fatalf("expired summary should make playtest slot: %+v %v", prepared, err)
		}
	})
}

func TestNaturalPeriodEndUsesPastPublicWithoutClosingState(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishWithAmounts(t, Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}})
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(3101)); err != nil {
		t.Fatal(err)
	}
	_, oldHash := fishCap(204)
	old, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: oldHash}, fishKey(3102))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Abandon(ctx, f.user, old.ID, AbandonInput{}, fishKey(3103)); err != nil {
		t.Fatal(err)
	}
	endMS := p.EndsAt * 1000
	f.tick(endMS - 1 - f.clock.Load())
	if page, err := f.s.ListPeriods(ctx, f.user, 1); err != nil || len(page.Items) != 1 {
		t.Fatalf("end-1 directory %+v %v", page, err)
	}
	if _, err := f.s.Period(ctx, f.user, period); err != nil {
		t.Fatalf("end-1 detail: %v", err)
	}
	if _, err := f.s.Node(ctx, f.user, period, node); err != nil {
		t.Fatalf("end-1 node: %v", err)
	}
	if _, err := f.s.Leaderboard(ctx, f.user, period, "", 1, 20); err != nil {
		t.Fatalf("end-1 board: %v", err)
	}
	_, currentHash := fishCap(205)
	current, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: currentHash}, fishKey(3104))
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int64{1, 1} {
		f.tick(delta)
		if page, err := f.s.ListPeriods(ctx, f.user, 1); err != nil || len(page.Items) != 0 {
			t.Fatalf("expired private directory %+v %v", page, err)
		}
		if _, err := f.s.Period(ctx, f.user, period); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired private detail: %v", err)
		}
		if _, err := f.s.Node(ctx, f.user, period, node); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired private node: %v", err)
		}
		if _, err := f.s.Leaderboard(ctx, f.user, period, "", 1, 20); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired private board: %v", err)
		}
		if view, err := f.s.CurrentChallenge(ctx, f.user, ""); err != nil || view.ID != current.ID {
			t.Fatalf("owned current after natural end: %+v %v", view, err)
		}
		if history, err := f.s.History(ctx, f.user, 20, 1); err != nil || len(history.Items) != 1 || history.Items[0].ID != old.ID {
			t.Fatalf("owned history after natural end: %+v %v", history, err)
		}
	}
	p, err = f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := f.s.SavePeriod(ctx, f.admin, period, PeriodInput{Title: p.Title, Description: p.Description, Visible: true, PastPublic: true, StartsAt: p.StartsAt, EndsAt: p.EndsAt, ExpectedRevision: p.Revision}, fishKey(3105))
	if err != nil || updated.State != "open" {
		t.Fatalf("past-public open period: %+v %v", updated, err)
	}
	if page, err := f.s.ListPeriods(ctx, f.user, 1); err != nil || len(page.Items) != 1 {
		t.Fatalf("past-public directory %+v %v", page, err)
	}
	if detail, err := f.s.Period(ctx, f.user, period); err != nil || detail.LeaderboardFinal {
		t.Fatalf("pending old challenge must keep board provisional: %+v %v", detail, err)
	}
	if _, err := f.s.Node(ctx, f.user, period, node); err != nil {
		t.Fatalf("past-public node: %v", err)
	}
	if board, err := f.s.Leaderboard(ctx, f.user, period, "", 1, 20); err != nil || board.Final {
		t.Fatalf("past-public provisional board: %+v %v", board, err)
	}
	if _, err := f.s.Abandon(ctx, f.user, current.ID, AbandonInput{}, fishKey(3106)); err != nil {
		t.Fatal(err)
	}
	if detail, err := f.s.Period(ctx, f.user, period); err != nil || !detail.LeaderboardFinal {
		t.Fatalf("finished natural-end period should have final board: %+v %v", detail, err)
	}
	if board, err := f.s.Leaderboard(ctx, f.user, period, "", 1, 20); err != nil || !board.Final {
		t.Fatalf("finished natural-end board: %+v %v", board, err)
	}
}

func TestPausingPeriodExpiresOnlyItsUnchargedPreparedChallenge(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(3201)); err != nil {
		t.Fatal(err)
	}
	before := f.balance(t)
	_, hash := fishCap(206)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(3202))
	if err != nil {
		t.Fatal(err)
	}
	input := PeriodInput{Title: p.Title, Description: p.Description, Visible: p.Visible, Paused: true, PastPublic: p.PastPublic, StartsAt: p.StartsAt, EndsAt: p.EndsAt, ExpectedRevision: p.Revision}
	pause, err := f.s.SavePeriod(ctx, f.admin, period, input, fishKey(3203))
	if err != nil {
		t.Fatal(err)
	}
	if view, err := f.s.Challenge(ctx, f.user, prepared.ID, ""); err != nil || view.State != "expired" || view.Result == nil || view.Result.TicketCharge != "0" {
		t.Fatalf("prepared pause result: %+v %v", view, err)
	}
	if got := f.balance(t); got != before {
		t.Fatalf("pause charged/refunded unstarted game: %s != %s", got, before)
	}
	if repeat, err := f.s.SavePeriod(ctx, f.admin, period, input, fishKey(3203)); err != nil || repeat.Revision != pause.Revision {
		t.Fatalf("pause receipt replay %+v %v", repeat, err)
	}
	if _, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(3204)); !errors.Is(err, ErrClosed) {
		t.Fatalf("paused period accepted new prepare: %v", err)
	}
	if progress, err := f.s.Node(ctx, f.user, period, node); err != nil || !progress.Progress.Unlocked {
		t.Fatalf("pause removed paid unlock: %+v %v", progress, err)
	}
	unpauseInput := PeriodInput{Title: p.Title, Description: p.Description, Visible: p.Visible, Paused: false, PastPublic: p.PastPublic, StartsAt: p.StartsAt, EndsAt: p.EndsAt, ExpectedRevision: pause.Revision}
	unpaused, err := f.s.SavePeriod(ctx, f.admin, period, unpauseInput, fishKey(3205))
	if err != nil {
		t.Fatal(err)
	}
	capability, hash := fishCap(207)
	second, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(3206))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, second.ID, StartInput{TabCapability: capability}, fishKey(3207))
	if err != nil {
		t.Fatal(err)
	}
	activePause := PeriodInput{Title: p.Title, Description: p.Description, Visible: p.Visible, Paused: true, PastPublic: p.PastPublic, StartsAt: p.StartsAt, EndsAt: p.EndsAt, ExpectedRevision: unpaused.Revision}
	pause, err = f.s.SavePeriod(ctx, f.admin, period, activePause, fishKey(3208))
	if err != nil {
		t.Fatal(err)
	}
	if view, err := f.s.Challenge(ctx, f.user, second.ID, ""); err != nil || view.State != "active" {
		t.Fatalf("pause interrupted paid active game: %+v %v", view, err)
	}
	if got := f.balance(t); got != "6500" {
		t.Fatalf("active ticket balance after pause: %s", got)
	}
	unpauseInput.ExpectedRevision = pause.Revision
	unpaused, err = f.s.SavePeriod(ctx, f.admin, period, unpauseInput, fishKey(3209))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	f.s.workerSlots <- struct{}{}
	f.s.workerSlots <- struct{}{}
	defer func() { <-f.s.workerSlots; <-f.s.workerSlots }()
	if view, err := f.s.Submit(ctx, f.user, second.ID, SubmitInput{TabCapability: capability, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(3210)); err != nil || view.State != "verifying" {
		t.Fatalf("queued verification %+v %v", view, err)
	}
	activePause.ExpectedRevision = unpaused.Revision
	if _, err := f.s.SavePeriod(ctx, f.admin, period, activePause, fishKey(3211)); err != nil {
		t.Fatal(err)
	}
	if view, err := f.s.Challenge(ctx, f.user, second.ID, ""); err != nil || view.State != "verifying" {
		t.Fatalf("pause interrupted verifying game: %+v %v", view, err)
	}
}

func TestExportFatFishUsesOneCombinedCollectionLimit(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishWithAmounts(t, Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}})
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(3301)); err != nil {
		t.Fatal(err)
	}
	makeSummary := func(index int) {
		t.Helper()
		_, hash := fishCap(byte(208 + index))
		prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: hash}, fishKey(3302+index*2))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.Abandon(ctx, f.user, prepared.ID, AbandonInput{}, fishKey(3303+index*2)); err != nil {
			t.Fatal(err)
		}
	}
	export := func(limit int) (lifecycle.FatFishExport, error) {
		t.Helper()
		tx, err := f.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		value, _, err := f.s.LifecycleAdapter().ExportFatFish(ctx, tx, lifecycle.ExportRequest{UserID: f.user, DecisionNow: f.clock.Load() / 1000, Limit: limit})
		return value, err
	}
	makeSummary(0)
	if result, err := export(2); err != nil || len(result.Summaries) != 1 || len(result.Progress) != 1 {
		t.Fatalf("exact combined limit: %+v %v", result, err)
	}
	if _, err := export(1); !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("one more than combined limit: %v", err)
	}
	makeSummary(1)
	if _, err := export(2); !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("second summary should exhaust shared limit: %v", err)
	}
	if result, err := export(3); err != nil || len(result.Summaries) != 2 || len(result.Progress) != 1 {
		t.Fatalf("combined limit 3: %+v %v", result, err)
	}
}

func TestLevelExportHTTPDocumentImportsWithoutEditing(t *testing.T) {
	f := newFishFixture(t)
	f.publishOne(t)
	var originalID string
	if err := f.db.QueryRow(`SELECT id FROM fatfish_levels LIMIT 1`).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	routes := &fishAdminRoutes{}
	if err := RegisterAdminRoutes(routes, f.s); err != nil {
		t.Fatal(err)
	}
	exportHandler := routes.routes[http.MethodGet+" "+adminPrefix+"/levels/{id}/export"]
	importHandler := routes.routes[http.MethodPost+" "+adminPrefix+"/levels/import"]
	if exportHandler == nil || importHandler == nil {
		t.Fatal("level export/import routes missing")
	}
	request := httptest.NewRequest(http.MethodGet, adminPrefix+"/levels/"+originalID+"/export", nil)
	request.SetPathValue("id", originalID)
	response := httptest.NewRecorder()
	exportHandler(response, request, limitedactivities.AdminPrincipal{UserID: f.admin})
	if response.Code != http.StatusOK {
		t.Fatalf("export HTTP %d: %s", response.Code, response.Body.String())
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil || len(fields) != 3 || fields["title"] == nil || fields["description"] == nil || fields["draft"] == nil {
		t.Fatalf("export must be pure import document: %v %+v", err, fields)
	}
	request = httptest.NewRequest(http.MethodPost, adminPrefix+"/levels/import", bytes.NewReader(response.Body.Bytes()))
	request.Header.Set("Idempotency-Key", fishKey(3501))
	importedResponse := httptest.NewRecorder()
	importHandler(importedResponse, request, limitedactivities.AdminPrincipal{UserID: f.admin})
	if importedResponse.Code != http.StatusOK {
		t.Fatalf("import of downloaded JSON %d: %s", importedResponse.Code, importedResponse.Body.String())
	}
	var imported LevelView
	if err := json.Unmarshal(importedResponse.Body.Bytes(), &imported); err != nil || imported.ID == "" || imported.ID == originalID {
		t.Fatalf("import response %+v %v", imported, err)
	}
	before, err := f.s.Level(context.Background(), f.admin, originalID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.s.Level(context.Background(), f.admin, imported.ID)
	if err != nil || after.Title != before.Title || after.Description != before.Description || !bytes.Equal(after.Draft, before.Draft) {
		t.Fatalf("round-trip level differs before=%+v after=%+v error=%v", before, after, err)
	}
}

func TestHTTPRejectsAmbiguousQueriesAndMalformedIdempotencyKeys(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishOne(t)
	p, err := f.s.AdminPeriod(context.Background(), f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	userRoutes := &fishUserRoutes{}
	adminRoutes := &fishAdminRoutes{}
	if err := RegisterUserRoutes(userRoutes, f.s); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(adminRoutes, f.s); err != nil {
		t.Fatal(err)
	}
	boardHandler := userRoutes.routes[http.MethodGet+" "+userPrefix+"/periods/{p}/leaderboard"]
	playtestsHandler := adminRoutes.routes[http.MethodGet+" "+adminPrefix+"/playtests"]
	for _, target := range []string{
		userPrefix + "/periods/" + period + "/leaderboard?node_id=" + node + "&node_id=" + node,
		userPrefix + "/periods/" + period + "/leaderboard?page=1&unexpected=1",
		userPrefix + "/periods/" + period + "/leaderboard?node_id=%ZZ",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.SetPathValue("p", period)
		response := httptest.NewRecorder()
		boardHandler(response, request, limitedactivities.UserPrincipal{UserID: f.user})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("ambiguous board query %s => %d", target, response.Code)
		}
	}
	for _, target := range []string{
		adminPrefix + "/playtests?version_id=" + p.Nodes[0].VersionID + "&version_id=" + p.Nodes[0].VersionID,
		adminPrefix + "/playtests?version_id=" + p.Nodes[0].VersionID + "&unexpected=1",
	} {
		response := httptest.NewRecorder()
		playtestsHandler(response, httptest.NewRequest(http.MethodGet, target, nil), limitedactivities.AdminPrincipal{UserID: f.admin})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("ambiguous playtest query %s => %d", target, response.Code)
		}
	}
	mutation := adminRoutes.routes[http.MethodPost+" "+adminPrefix+"/levels"]
	for _, key := range []string{"short", "fatfish-idempotency-invalid key", strings.Repeat("A", 129)} {
		request := httptest.NewRequest(http.MethodPost, adminPrefix+"/levels", strings.NewReader(`{}`))
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		mutation(response, request, limitedactivities.AdminPrincipal{UserID: f.admin})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed idempotency key %q => %d", key, response.Code)
		}
	}
}

func TestHTTPMapsRealAuthorityAndMaintenanceErrors(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishWithAmounts(t, Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}})
	p, err := f.s.AdminPeriod(context.Background(), f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	userRoutes := &fishUserRoutes{}
	adminRoutes := &fishAdminRoutes{}
	if err := RegisterUserRoutes(userRoutes, f.s); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(adminRoutes, f.s); err != nil {
		t.Fatal(err)
	}
	check := func(response *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
			t.Fatalf("HTTP error status=%d body=%s, want %d/%s", response.Code, response.Body.String(), status, code)
		}
	}
	adminList := adminRoutes.routes[http.MethodGet+" "+adminPrefix+"/levels"]
	f.s.admins = rejectingAuthority{err: authz.ErrForbidden}
	response := httptest.NewRecorder()
	adminList(response, httptest.NewRequest(http.MethodGet, adminPrefix+"/levels", nil), limitedactivities.AdminPrincipal{UserID: f.admin})
	check(response, http.StatusForbidden, httperr.CodeForbidden)
	f.s.admins = rejectingAuthority{err: authz.ErrElevatedRequired}
	response = httptest.NewRecorder()
	adminList(response, httptest.NewRequest(http.MethodGet, adminPrefix+"/levels", nil), limitedactivities.AdminPrincipal{UserID: f.admin})
	check(response, http.StatusForbidden, httperr.CodeElevationRequired)
	f.s.admins = testAuthority{}
	userList := userRoutes.routes[http.MethodGet+" "+userPrefix+"/periods"]
	f.s.users = rejectingAuthority{err: authz.ErrUnauthorized}
	response = httptest.NewRecorder()
	userList(response, httptest.NewRequest(http.MethodGet, userPrefix+"/periods", nil), limitedactivities.UserPrincipal{UserID: f.user})
	check(response, http.StatusUnauthorized, httperr.CodeUnauthorized)
	f.s.users = testAuthority{}
	unlock := userRoutes.routes[http.MethodPost+" "+userPrefix+"/periods/{p}/nodes/{n}/unlock"]
	for index, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{maintenance.ErrMaintenanceOn, http.StatusServiceUnavailable, httperr.CodeMaintenance},
		{resources.ErrMaintenance, http.StatusServiceUnavailable, httperr.CodeMaintenance},
		{resources.ErrForbidden, http.StatusForbidden, httperr.CodeForbidden},
		{limitedactivities.ErrClosed, http.StatusForbidden, httperr.CodeFeatureDisabled},
	} {
		f.s.gate = rejectingGate{err: tc.err}
		body, _ := json.Marshal(UnlockInput{ExpectedRevision: p.Nodes[0].Revision})
		request := httptest.NewRequest(http.MethodPost, userPrefix+"/periods/"+period+"/nodes/"+node+"/unlock", bytes.NewReader(body))
		request.SetPathValue("p", period)
		request.SetPathValue("n", node)
		request.Header.Set("Idempotency-Key", fishKey(3601+index))
		response = httptest.NewRecorder()
		unlock(response, request, limitedactivities.UserPrincipal{UserID: f.user})
		check(response, tc.status, tc.code)
	}
}

func TestSubmitHTTPStatusTracksVerifyingAndTerminalForBothRoles(t *testing.T) {
	f := newFishFixture(t)
	period, node := f.publishWithAmounts(t, Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}})
	ctx := context.Background()
	p, err := f.s.AdminPeriod(ctx, f.admin, period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unlock(ctx, f.user, period, node, UnlockInput{ExpectedRevision: p.Nodes[0].Revision}, fishKey(3701)); err != nil {
		t.Fatal(err)
	}
	userRoutes := &fishUserRoutes{}
	adminRoutes := &fishAdminRoutes{}
	if err := RegisterUserRoutes(userRoutes, f.s); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(adminRoutes, f.s); err != nil {
		t.Fatal(err)
	}
	blockWorkers := func() func() {
		f.s.workerSlots <- struct{}{}
		f.s.workerSlots <- struct{}{}
		released := false
		return func() {
			if !released {
				released = true
				<-f.s.workerSlots
				<-f.s.workerSlots
			}
		}
	}
	userCap, userHash := fishCap(220)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: period, NodeID: node, ExpectedRevision: p.Nodes[0].Revision, TabCapabilityHash: userHash}, fishKey(3702))
	if err != nil {
		t.Fatal(err)
	}
	started, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: userCap}, fishKey(3703))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*started.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	userRaw, _ := json.Marshal(map[string]any{"tab_capability": userCap, "inputs": []any{}, "terminal_tick": f.terminalTick})
	userHandler := userRoutes.routes[http.MethodPost+" "+userPrefix+"/challenges/{id}/submit"]
	userRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, userPrefix+"/challenges/"+prepared.ID+"/submit", bytes.NewReader(userRaw))
		r.SetPathValue("id", prepared.ID)
		r.Header.Set("Idempotency-Key", fishKey(3704))
		return r
	}
	releaseUser := blockWorkers()
	defer releaseUser()
	response := httptest.NewRecorder()
	userHandler(response, userRequest(), limitedactivities.UserPrincipal{UserID: f.user})
	if response.Code != http.StatusAccepted {
		t.Fatalf("user verifying submit HTTP %d: %s", response.Code, response.Body.String())
	}
	releaseUser()
	f.waitState(t, prepared.ID, "settled_pass")
	response = httptest.NewRecorder()
	userHandler(response, userRequest(), limitedactivities.UserPrincipal{UserID: f.user})
	if response.Code != http.StatusOK {
		t.Fatalf("user terminal submit HTTP %d: %s", response.Code, response.Body.String())
	}
	adminCap, adminHash := fishCap(221)
	adminPrepared, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: p.Nodes[0].VersionID, TabCapabilityHash: adminHash}, fishKey(3705))
	if err != nil {
		t.Fatal(err)
	}
	adminStarted, err := f.s.StartPlaytest(ctx, f.admin, adminPrepared.ID, StartInput{TabCapability: adminCap}, fishKey(3706))
	if err != nil {
		t.Fatal(err)
	}
	f.tick(*adminStarted.StartAtMS + int64(f.terminalTick)*1000/60 - f.clock.Load())
	adminRaw, _ := json.Marshal(map[string]any{"tab_capability": adminCap, "inputs": []any{}, "terminal_tick": f.terminalTick})
	adminHandler := adminRoutes.routes[http.MethodPost+" "+adminPrefix+"/playtests/{id}/submit"]
	adminRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, adminPrefix+"/playtests/"+adminPrepared.ID+"/submit", bytes.NewReader(adminRaw))
		r.SetPathValue("id", adminPrepared.ID)
		r.Header.Set("Idempotency-Key", fishKey(3707))
		return r
	}
	releaseAdmin := blockWorkers()
	defer releaseAdmin()
	response = httptest.NewRecorder()
	adminHandler(response, adminRequest(), limitedactivities.AdminPrincipal{UserID: f.admin})
	if response.Code != http.StatusAccepted {
		t.Fatalf("admin verifying submit HTTP %d: %s", response.Code, response.Body.String())
	}
	releaseAdmin()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, err := f.s.PlaytestChallenge(ctx, f.admin, adminPrepared.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if view.State == "settled_pass" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	response = httptest.NewRecorder()
	adminHandler(response, adminRequest(), limitedactivities.AdminPrincipal{UserID: f.admin})
	if response.Code != http.StatusOK {
		t.Fatalf("admin terminal submit HTTP %d: %s", response.Code, response.Body.String())
	}
}
