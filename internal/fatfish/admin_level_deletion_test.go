package fatfish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

func TestDeleteLevelRevisionReplayAndRollback(t *testing.T) {
	f := newFishFixture(t)
	ctx := context.Background()
	level, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Discard draft", Draft: f.level}, fishKey(5100))
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []int64{0, f.user} {
		if _, err := f.s.DeleteLevel(ctx, actor, level.ID, "1", fishKey(5101)); !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin deletion accepted: %v", err)
		}
	}
	if _, err := f.s.DeleteLevel(ctx, f.admin, level.ID, "2", fishKey(5102)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER reject_library_deletion BEFORE INSERT ON fatfish_deleted_levels BEGIN SELECT RAISE(ABORT,'injected deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DeleteLevel(ctx, f.admin, level.ID, "1", fishKey(5103)); err == nil || !strings.Contains(err.Error(), "injected deletion failure") {
		t.Fatalf("injection did not run: %v", err)
	}
	if retained, err := f.s.Level(ctx, f.admin, level.ID); err != nil || retained.Revision != "1" {
		t.Fatal("failed deletion changed draft revision", retained, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_library_deletion`); err != nil {
		t.Fatal(err)
	}
	deleted, err := f.s.DeleteLevel(ctx, f.admin, level.ID, "1", fishKey(5103))
	if err != nil || deleted.ID != level.ID || deleted.Revision != "2" || deleted.DeletedAt != fixtureNow {
		t.Fatal(deleted, err)
	}
	if replay, err := f.s.DeleteLevel(ctx, f.admin, level.ID, "1", fishKey(5103)); err != nil || replay != deleted {
		t.Fatal("deletion replay changed", replay, err)
	}
	if _, err := f.s.DeleteLevel(ctx, f.admin, level.ID, "2", fishKey(5103)); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused key accepted changed body: %v", err)
	}
	if _, err := f.s.Level(ctx, f.admin, level.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted draft readable: %v", err)
	}
	if _, err := f.s.PublishVersion(ctx, f.admin, level.ID, "2", fishKey(5104)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted draft published: %v", err)
	}
	if _, err := f.s.SaveLevel(ctx, f.admin, level.ID, LevelInput{Title: "Resurrection", Draft: f.level, ExpectedRevision: "2"}, fishKey(5105)); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted draft restored by stale editor: %v", err)
	}
	page, err := f.s.Levels(ctx, f.admin, 1)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("deleted level remains in library", page, err)
	}
}

func TestDeleteLevelPreservesActivityAndAcceptedChallenge(t *testing.T) {
	f := newFishFixture(t)
	ctx := context.Background()
	zero := Amounts{UnlockCost: "0", TicketPrice: "0", FirstClearReward: "0", StarRewards: [3]string{"0", "0", "0"}}
	periodID, nodeID := f.publishWithAmounts(t, zero)
	period, err := f.s.AdminPeriod(ctx, f.admin, periodID)
	if err != nil {
		t.Fatal(err)
	}
	node := period.Nodes[0]
	version, err := f.s.Version(ctx, f.admin, node.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	level, err := f.s.Level(ctx, f.admin, version.LevelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Unlock(ctx, f.user, periodID, nodeID, UnlockInput{ExpectedRevision: node.Revision}, fishKey(5201)); err != nil {
		t.Fatal(err)
	}
	capability, hash := fishCap(202)
	prepared, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: periodID, NodeID: nodeID, ExpectedRevision: node.Revision, TabCapabilityHash: hash}, fishKey(5202))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DeleteLevel(ctx, f.admin, level.ID, level.Revision, fishKey(5203)); err != nil {
		t.Fatal(err)
	}
	if retained, err := f.s.Version(ctx, f.admin, version.ID); err != nil || !reflect.DeepEqual(retained, version) {
		t.Fatal("deletion changed immutable content", err)
	}
	if _, err := f.s.Start(ctx, f.user, prepared.ID, StartInput{TabCapability: capability}, fishKey(5204)); err != nil {
		t.Fatal("accepted challenge cannot start after deletion", err)
	}
	f.tick(3200)
	if _, err := f.s.Submit(ctx, f.user, prepared.ID, SubmitInput{TabCapability: capability, Inputs: []byte(`[]`), TerminalTick: f.terminalTick}, fishKey(5205)); err != nil {
		t.Fatal("accepted challenge cannot settle after deletion", err)
	}
	f.waitState(t, prepared.ID, "settled_pass")
	if _, err := f.s.Prepare(ctx, f.user, PrepareInput{PeriodID: periodID, NodeID: nodeID, ExpectedRevision: node.Revision, TabCapabilityHash: hash}, fishKey(5206)); err != nil {
		t.Fatal("existing activity node stopped accepting play", err)
	}
	if _, err := f.s.PreparePlaytest(ctx, f.admin, PlaytestInput{VersionID: version.ID, TabCapabilityHash: hash}, fishKey(5207)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted library source accepted a new playtest: %v", err)
	}
	input := NodeInput{Title: "Retained", VersionID: version.ID, Condition: json.RawMessage(`{}`), Amounts: zero, ExpectedPeriodRevision: period.Revision}
	if _, err := f.s.SaveNode(ctx, f.admin, periodID, "", input, fishKey(5208)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted library source assigned to a new node: %v", err)
	}
	input.ExpectedRevision = node.Revision
	if _, err := f.s.SaveNode(ctx, f.admin, periodID, nodeID, input, fishKey(5209)); err != nil {
		t.Fatal("existing node cannot retain its published version", err)
	}
}

func TestDeleteLevelCompetesWithDraftSave(t *testing.T) {
	f := newFishFixture(t)
	ctx := context.Background()
	level, err := f.s.SaveLevel(ctx, f.admin, "", LevelInput{Title: "Original", Draft: f.level}, fishKey(5300))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		_, err := f.s.DeleteLevel(ctx, f.admin, level.ID, "1", fishKey(5301))
		results <- err
	}()
	go func() {
		defer workers.Done()
		<-start
		_, err := f.s.SaveLevel(ctx, f.admin, level.ID, LevelInput{Title: "Saved", Draft: f.level, ExpectedRevision: "1"}, fishKey(5302))
		results <- err
	}()
	close(start)
	workers.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("competing edits: success=%d conflict=%d", success, conflict)
	}
	var revision, markers int
	if err := f.db.QueryRow(`SELECT revision,(SELECT count(*) FROM fatfish_deleted_levels WHERE level_id=?) FROM fatfish_levels WHERE id=?`, level.ID, level.ID).Scan(&revision, &markers); err != nil || revision != 2 || markers > 1 {
		t.Fatal("revision/marker not linearized", revision, markers, err)
	}
}

func TestDeleteLevelHTTPValidationAndPermission(t *testing.T) {
	f := newFishFixture(t)
	level, err := f.s.SaveLevel(context.Background(), f.admin, "", LevelInput{Title: "HTTP draft", Draft: f.level}, fishKey(5400))
	if err != nil {
		t.Fatal(err)
	}
	routes := &fishAdminRoutes{}
	if err := RegisterAdminRoutes(routes, f.s); err != nil {
		t.Fatal(err)
	}
	handler := routes.routes[http.MethodDelete+" "+adminPrefix+"/levels/{id}"]
	if handler == nil {
		t.Fatal("delete route missing")
	}
	for _, tc := range []struct {
		body   string
		actor  int64
		status int
	}{
		{`{"expected_revision":"1"}`, f.user, http.StatusForbidden},
		{`{"expected_revision":"1","extra":true}`, f.admin, http.StatusBadRequest},
		{`{}`, f.admin, http.StatusBadRequest},
		{`{"expected_revision":"1"}`, f.admin, http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodDelete, adminPrefix+"/levels/"+level.ID, bytes.NewBufferString(tc.body))
		request.Header.Set("Idempotency-Key", fishKey(5401))
		request.SetPathValue("id", level.ID)
		response := httptest.NewRecorder()
		handler(response, request, limitedactivities.AdminPrincipal{UserID: tc.actor})
		if response.Code != tc.status {
			t.Fatalf("body=%s actor=%d HTTP %d, want %d: %s", tc.body, tc.actor, response.Code, tc.status, response.Body.String())
		}
	}
}
