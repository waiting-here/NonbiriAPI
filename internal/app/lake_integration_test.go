package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lakenotes"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestLakeRootRecoveryMaintenanceExportAndDeletion(t *testing.T) {
	f := newActivityWireFixture(t)
	const base = "/api/limited-activities/lake-notes"
	const adminBase = "/admin/api/limited-activities/lake-notes"
	call := func(method, path string, body any, seat int, out any) {
		t.Helper()
		cookie := f.adminCookie
		if seat >= 0 {
			cookie = f.users[seat].Cookie
		}
		r := f.request(method, path, body, cookie, seat < 0)
		if r.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, r.Code, r.Body.String())
		}
		if err := json.Unmarshal(r.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	var profile lakenotes.ProfileView
	call("GET", base+"/profile", nil, 0, &profile)
	if !profile.Readonly || profile.Period != nil || profile.Entitlement != nil || profile.Profile.Coins != "0" {
		t.Fatalf("default activity is open or funded: %+v", profile)
	}
	fee := "1000"
	var period lakenotes.Period
	call("POST", adminBase+"/periods", lakenotes.PeriodInput{
		ExpectedRevision: "0", Name: "Summer waters", Status: "published",
		StartsAt: f.now().Unix() - 10, EndsAt: f.now().Unix() + 3600, EntryFeeMilli: &fee,
		Exchanges: map[lakenotes.Direction]lakenotes.ExchangeSetting{
			lakenotes.GeneralToCoins: {Enabled: true, SourceAmount: "1000", TargetAmount: "3"},
		},
	}, -1, &period)
	config := f.call("GET", adminBase, nil, f.adminCookie, true)
	f.call("PUT", adminBase, map[string]any{
		"expected_revision": config["revision"], "visible": true, "paused": false,
		"starts_at": nil, "ends_at": nil, "module_config": map[string]any{},
	}, f.adminCookie, true)
	var entry lakenotes.EntryResult
	call("POST", base+"/entry", lakenotes.EntryInput{PeriodID: period.ID, ExpectedPeriodRevision: period.Revision}, 0, &entry)
	var repeatEntry lakenotes.EntryResult
	call("POST", base+"/entry", lakenotes.EntryInput{PeriodID: period.ID, ExpectedPeriodRevision: period.Revision}, 0, &repeatEntry)
	if repeatEntry.Receipt.OperationID != entry.Receipt.OperationID || repeatEntry.Profile.Wallet != entry.Profile.Wallet {
		t.Fatal("re-entering charged another fee")
	}
	var exchange lakenotes.ExchangeResult
	call("POST", base+"/exchange", lakenotes.ExchangeInput{
		QuoteInput:             lakenotes.QuoteInput{Direction: lakenotes.GeneralToCoins, Quantity: "2", PeriodID: period.ID},
		ExpectedPeriodRevision: period.Revision, ExpectedProfileRevision: entry.Profile.Revision,
	}, 0, &exchange)
	if exchange.Profile.Profile.Coins != "6" || exchange.Receipt.SourceAmount != "2000" || exchange.Receipt.TargetAmount != "6" {
		t.Fatalf("exchange did not use exact lots: %+v", exchange)
	}
	var started, current lakenotes.CastResult
	call("POST", base+"/casts", lakenotes.StartInput{ExpectedProfileRevision: exchange.Profile.Revision}, 0, &started)
	call("POST", base+"/casts/"+started.Cast.ID+"/checkpoint", lakenotes.CheckpointInput{
		ControlInput: lakenotes.ControlInput{Generation: started.Cast.Generation, ExpectedRevision: started.Cast.Revision},
		FromTick:     1, ToTick: 1, Edges: []lakenotes.Edge{},
	}, 0, &current)
	call("POST", base+"/entry", lakenotes.EntryInput{PeriodID: period.ID, ExpectedPeriodRevision: period.Revision}, 1, &entry)
	var other lakenotes.CastResult
	call("POST", base+"/casts", lakenotes.StartInput{ExpectedProfileRevision: entry.Profile.Revision}, 1, &other)
	var paused int
	var userRevision []byte
	if err := f.store.DB().QueryRow("SELECT revision FROM users WHERE id=?", f.users[1].ID).Scan(&userRevision); err != nil {
		t.Fatal(err)
	}
	revision, err := db.DecodeU128(userRevision)
	if err != nil {
		t.Fatal(err)
	}
	ban := f.request("POST", "/admin/api/users/"+f.users[1].ID+"/ban", map[string]any{"expected_revision": revision.Decimal(), "reason": "policy", "duration_seconds": nil}, f.adminCookie, true)
	if ban.Code != http.StatusNoContent {
		t.Fatalf("ban: %d %s", ban.Code, ban.Body.String())
	}
	if err := f.store.DB().QueryRow("SELECT paused FROM lake_notes_casts WHERE id=?", other.Cast.ID).Scan(&paused); err != nil || paused != 1 {
		t.Fatalf("ban did not pause cast: %d %v", paused, err)
	}
	var elapsedBefore, elapsedAfter int64
	if err := f.store.DB().QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", current.Cast.ID).Scan(&elapsedBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.closeApplication(); err != nil {
		t.Fatal(err)
	}
	f.offset.Add(300)
	if err := f.open(); err != nil {
		t.Fatal(err)
	}
	call("GET", base+"/casts/"+started.Cast.ID, nil, 0, &current)
	if !current.Cast.Paused || current.Cast.AckTick != 1 || current.Cast.State.Plan != started.Cast.State.Plan || current.Profile.Profile.Coins != "6" {
		t.Fatalf("startup lost the confirmed cast: %+v", current)
	}
	if err := f.store.DB().QueryRow("SELECT active_elapsed_ns FROM lake_notes_casts WHERE id=?", current.Cast.ID).Scan(&elapsedAfter); err != nil || elapsedBefore != elapsedAfter {
		t.Fatalf("startup accrued offline time: %d -> %d, %v", elapsedBefore, elapsedAfter, err)
	}
	call("POST", base+"/casts/"+current.Cast.ID+"/resume", lakenotes.ControlInput{Generation: current.Cast.Generation, ExpectedRevision: current.Cast.Revision}, 0, &current)
	call("POST", base+"/entry", lakenotes.EntryInput{PeriodID: period.ID, ExpectedPeriodRevision: period.Revision}, 2, &entry)
	call("POST", base+"/casts", lakenotes.StartInput{ExpectedProfileRevision: entry.Profile.Revision}, 2, &other)
	f.offset.Add(10)
	work, err := f.app.Load().activityRuntime.Retain(context.Background(), f.now().Unix(), 1, time.Now().Add(time.Second))
	if err != nil || work.Processed != 1 || !work.More {
		t.Fatalf("shared retention budget: %+v %v", work, err)
	}
	paused = 0
	for batch := 0; batch < 16; batch++ {
		before := paused
		if err := f.store.DB().QueryRow("SELECT count(*) FROM lake_notes_casts WHERE paused=1 AND id IN (?,?)", current.Cast.ID, other.Cast.ID).Scan(&paused); err != nil || paused > before+1 {
			t.Fatalf("retention exceeded its row budget: %d -> %d, %v", before, paused, err)
		}
		if paused == 2 {
			break
		}
		work, err = f.app.Load().activityRuntime.Retain(context.Background(), f.now().Unix(), 1, time.Now().Add(time.Second))
		if err != nil || work.Processed != 1 {
			t.Fatalf("retention stopped before reaching expired casts: %+v %v", work, err)
		}
	}
	if paused != 2 {
		t.Fatal("retention did not reach both expired casts")
	}
	f.call("POST", "/admin/api/maintenance/enable", map[string]any{
		"expected_revision": "2", "reason": "planned maintenance", "confirmation": true,
	}, f.adminCookie, true)
	call("GET", base+"/casts/"+current.Cast.ID, nil, 0, &current)
	if !current.Cast.Paused || !current.Profile.Readonly {
		t.Fatal("maintenance did not pause the cast")
	}
	call("POST", base+"/casts/"+current.Cast.ID+"/pause", lakenotes.ControlInput{Generation: current.Cast.Generation, ExpectedRevision: current.Cast.Revision}, 0, &current)
	if r := f.request("POST", base+"/casts/"+current.Cast.ID+"/resume", lakenotes.ControlInput{Generation: current.Cast.Generation, ExpectedRevision: current.Cast.Revision}, f.users[0].Cookie, false); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("maintenance admitted resume: %d %s", r.Code, r.Body.String())
	}
	f.call("POST", "/admin/api/maintenance/disable", map[string]any{"expected_revision": "3", "reason": "continue"}, f.adminCookie, true)
	userID, err := strconv.ParseInt(f.users[0].ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	binding := sha256.Sum256([]byte(f.users[0].Cookie.Value))
	token, _, err := f.app.Load().authRuntime.ElevationManager().IssueBound(userID, elevation.KindUser, hex.EncodeToString(binding[:]))
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + f.cfg.UserHost, "X-Elevated-Token": token}
	exported := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/account/export", "", []*http.Cookie{f.users[0].Cookie}, headers)
	var document lifecycle.ExportDocument
	if exported.Code != http.StatusOK || json.Unmarshal(exported.Body.Bytes(), &document) != nil {
		t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
	}
	lake := document.LakeNotes
	if document.SchemaVersion != lifecycle.SchemaVersion || lake.Profile.Coins != "6" || len(lake.Casts) != 1 || lake.Casts[0].AckTick != 1 || len(lake.Entries) != 1 || len(lake.Exchanges) != 1 || lake.Exchanges[0].SourceAmount != "2000" || lake.Entries[0].OperationID != repeatEntry.Receipt.OperationID {
		t.Fatalf("export omitted personal progress or receipts: %+v", lake)
	}
	for _, internal := range []string{"reward_plan", "request_hash", "active_elapsed_ns", "lease_until_ns"} {
		if strings.Contains(exported.Body.String(), `"`+internal+`"`) {
			t.Fatalf("export leaked %s", internal)
		}
	}
	token, _, err = f.app.Load().authRuntime.ElevationManager().IssueBound(userID, elevation.KindUser, hex.EncodeToString(binding[:]))
	if err != nil {
		t.Fatal(err)
	}
	headers["X-Elevated-Token"] = token
	deleted := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, "/api/account/delete", wireBody(t, map[string]string{"confirm": "DELETE"}), []*http.Cookie{f.users[0].Cookie}, headers)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	for _, table := range []string{"lake_notes_profiles", "lake_notes_casts", "lake_notes_entitlements", "lake_notes_exchange_receipts"} {
		var count int
		if err := f.store.DB().QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", userID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("delete left %s rows: %d %v", table, count, err)
		}
	}
	actor, err := idempotency.ActorScopeHash("user", f.users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var receipts, operations int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM idempotency_records WHERE scope=? AND actor_scope_hash=?", string(idempotency.ScopeLakeNotes), actor[:]).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("delete left personal receipts: %d %v", receipts, err)
	}
	if err := f.store.DB().QueryRow("SELECT count(*) FROM credit_operations WHERE id IN (?,?)", repeatEntry.Receipt.OperationID, exchange.Receipt.OperationID).Scan(&operations); err != nil || operations != 2 {
		t.Fatalf("delete removed settled ledger operations: %d %v", operations, err)
	}
	if late := f.request("POST", base+"/casts/"+current.Cast.ID+"/resume", lakenotes.ControlInput{Generation: current.Cast.Generation, ExpectedRevision: current.Cast.Revision}, f.users[0].Cookie, false); late.Code != http.StatusUnauthorized {
		t.Fatalf("deleted account resumed a cast: %d %s", late.Code, late.Body.String())
	}
}
