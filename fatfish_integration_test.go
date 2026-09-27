package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/fatfish"
)

const fishUserPath = "/api/limited-activities/fat-fish"
const fishAdminPath = "/admin/api/limited-activities/fat-fish"

func decodeFishResponse[T any](t *testing.T, response *httptest.ResponseRecorder, want int) T {
	t.Helper()
	var result T
	if response.Code != want || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("fish response: %d %s", response.Code, response.Body.String())
	}
	return result
}

func fishWireCapability(value byte) (string, string) {
	raw := bytes.Repeat([]byte{value}, 32)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(raw), hex.EncodeToString(digest[:])
}

func publishFishWireFixture(t *testing.T, f *duelWireFixture) (fatfish.NodeView, int) {
	t.Helper()
	raw, err := os.ReadFile("web/src/shared/fatfish/engine/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Level  json.RawMessage `json:"level"`
			Result struct {
				TerminalTick int `json:"terminal_tick"`
			} `json:"result"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil || len(vectors.Cases) == 0 {
		t.Fatal("missing engine vector", err)
	}
	level := decodeFishResponse[fatfish.LevelView](t, f.admin("POST", fishAdminPath+"/levels", fatfish.LevelInput{Title: "Integration pond", Draft: vectors.Cases[0].Level}, 200), 200)
	version := decodeFishResponse[fatfish.VersionView](t, f.admin("POST", fishAdminPath+"/levels/"+level.ID+"/versions", map[string]string{"expected_revision": level.Revision}, 200), 200)
	capability, hash := fishWireCapability(21)
	prepared := decodeFishResponse[fatfish.ChallengeView](t, f.admin("POST", fishAdminPath+"/playtests", fatfish.PlaytestInput{VersionID: version.ID, TabCapabilityHash: hash}, 200), 200)
	started := decodeFishResponse[fatfish.ChallengeView](t, f.admin("POST", fishAdminPath+"/playtests/"+prepared.ID+"/start", fatfish.StartInput{TabCapability: capability}, 200), 200)
	if started.Seed == "" || started.StartAtMS == nil {
		t.Fatal("missing original playtest receipt")
	}
	f.clock.Store((*started.StartAtMS + int64(vectors.Cases[0].Result.TerminalTick)*1000/60 + 999) / 1000)
	f.admin("POST", fishAdminPath+"/playtests/"+prepared.ID+"/submit", map[string]any{"tab_capability": capability, "inputs": []any{}, "terminal_tick": vectors.Cases[0].Result.TerminalTick}, 202)
	deadline := time.Now().Add(10 * time.Second)
	for {
		result := decodeFishResponse[fatfish.ChallengeView](t, f.admin("GET", fishAdminPath+"/playtests/"+prepared.ID, nil, 200), 200)
		if result.State == "settled_pass" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("playtest did not settle: %+v", result)
		}
		time.Sleep(10 * time.Millisecond)
	}
	now := f.clock.Load()
	if _, err := f.store.DB().Exec(`UPDATE limited_activity_configs SET visible=1,starts_at=?,ends_at=?,paused=0,revision=revision+1 WHERE activity_key='fat-fish'`, now-10, now+3600); err != nil {
		t.Fatal(err)
	}
	period := decodeFishResponse[fatfish.PeriodView](t, f.admin("POST", fishAdminPath+"/periods", fatfish.PeriodInput{Title: "Integration season", Visible: true, StartsAt: now - 10, EndsAt: now + 3600}, 200), 200)
	node := decodeFishResponse[fatfish.NodeView](t, f.admin("POST", fishAdminPath+"/periods/"+period.ID+"/nodes", fatfish.NodeInput{
		Title: "Integration node", VersionID: version.ID, Condition: json.RawMessage(`{}`), ExpectedPeriodRevision: period.Revision,
		Amounts: fatfish.Amounts{UnlockCost: "1", TicketPrice: "2", FirstClearReward: "3", StarRewards: [3]string{"1", "2", "3"}},
	}, 200), 200)
	period = decodeFishResponse[fatfish.PeriodView](t, f.admin("GET", fishAdminPath+"/periods/"+period.ID, nil, 200), 200)
	f.admin("POST", fishAdminPath+"/periods/"+period.ID+"/publish", map[string]string{"expected_revision": period.Revision}, 200)
	return node, vectors.Cases[0].Result.TerminalTick
}

func startFishWireChallenge(t *testing.T, f *duelWireFixture, node fatfish.NodeView, seat int) (fatfish.ChallengeView, string) {
	t.Helper()
	decodeFishResponse[fatfish.Progress](t, f.call(seat, "POST", fishUserPath+"/periods/"+node.PeriodID+"/nodes/"+node.ID+"/unlock", fatfish.UnlockInput{ExpectedRevision: node.Revision}, false), 200)
	capability, hash := fishWireCapability(byte(41 + seat))
	prepared := decodeFishResponse[fatfish.ChallengeView](t, f.call(seat, "POST", fishUserPath+"/challenges/prepare", fatfish.PrepareInput{PeriodID: node.PeriodID, NodeID: node.ID, ExpectedRevision: node.Revision, TabCapabilityHash: hash}, false), 200)
	if prepared.Seed != "" {
		t.Fatal("prepare revealed seed")
	}
	started := decodeFishResponse[fatfish.ChallengeView](t, f.call(seat, "POST", fishUserPath+"/challenges/"+prepared.ID+"/start", fatfish.StartInput{TabCapability: capability}, false), 200)
	if started.Seed == "" || started.StartAtMS == nil {
		t.Fatal("start omitted original receipt")
	}
	return started, capability
}

func TestFatFishProductionMaintenanceExportAndDeletion(t *testing.T) {
	f := newDuelWireFixture(t)
	node, terminalTick := publishFishWireFixture(t, f)
	started, capability := startFishWireChallenge(t, f, node, 0)
	if r := f.call(1, "GET", fishUserPath+"/challenges/"+started.ID, nil, false); r.Code != 404 {
		t.Fatalf("cross-owner read: %d", r.Code)
	}
	read := decodeFishResponse[fatfish.ChallengeView](t, f.call(0, "GET", fishUserPath+"/challenges/"+started.ID, nil, false), 200)
	if read.Seed != "" || read.Level != nil {
		t.Fatal("cookie alone exposed recovery material")
	}
	f.admin("POST", "/admin/api/maintenance/enable", map[string]any{"expected_revision": "2", "reason": "integration", "confirmation": true}, 200)
	_, hash := fishWireCapability(50)
	if r := f.call(1, "POST", fishUserPath+"/challenges/prepare", fatfish.PrepareInput{PeriodID: node.PeriodID, NodeID: node.ID, ExpectedRevision: node.Revision, TabCapabilityHash: hash}, false); r.Code != 503 {
		t.Fatalf("new work during maintenance: %d %s", r.Code, r.Body.String())
	}
	f.clock.Store((*started.StartAtMS + int64(terminalTick)*1000/60 + 999) / 1000)
	decodeFishResponse[fatfish.ChallengeView](t, f.call(0, "POST", fishUserPath+"/challenges/"+started.ID+"/submit", map[string]any{"tab_capability": capability, "inputs": []any{}, "terminal_tick": terminalTick}, false), 202)
	deadline := time.Now().Add(10 * time.Second)
	for {
		read = decodeFishResponse[fatfish.ChallengeView](t, f.call(0, "GET", fishUserPath+"/challenges/"+started.ID, nil, false), 200)
		if read.State == "settled_pass" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("continuation did not settle: %+v", read)
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.admin("POST", "/admin/api/maintenance/disable", map[string]any{"expected_revision": "3", "reason": "integration complete"}, 200)
	document := readPersonalExport(t, f, 0)
	if len(document.FatFish.Summaries) != 1 || len(document.FatFish.Progress) != 1 || !document.FatFish.Summaries[0].Passed || document.FatFish.Summaries[0].Rewards != read.Result.Rewards {
		t.Fatalf("safe fish export missing settled fact: %+v", document.FatFish)
	}
	if len(readPersonalExport(t, f, 1).FatFish.Progress) != 0 {
		t.Fatal("export crossed owner boundary")
	}
	raw, _ := json.Marshal(document.FatFish)
	for _, forbidden := range []string{started.Seed, capability, `"inputs"`, `"tab_capability_hash"`, `"identity_key"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("private recovery data exported")
		}
	}
	_, _ = startFishWireChallenge(t, f, node, 1)
	before := readPersonalExport(t, f, 1).User.Balance
	response := f.call(1, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
	if response.Code != 204 {
		t.Fatalf("delete live challenge: %d %s", response.Code, response.Body.String())
	}
	var balance string
	if err := f.store.DB().QueryRow(`SELECT json_extract(snapshot_json,'$.general_balance') FROM admin_account_deletions WHERE former_user_id=?`, f.users[1]).Scan(&balance); err != nil || balance != before {
		t.Fatalf("self deletion refunded ticket: %q want %q err=%v", balance, before, err)
	}
	for _, table := range []string{"fatfish_challenges", "fatfish_progress", "fatfish_period_progress"} {
		var count int
		if err := f.store.DB().QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, f.users[1]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("personal fish rows survived deletion: %s %d %v", table, count, err)
		}
	}
	f.checkLedger()
}

func TestInteractionProductionExportRedactsAdaptationAndIdentity(t *testing.T) {
	f := newDuelWireFixture(t)
	now := f.clock.Load()
	result, err := f.store.DB().Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible','https://export.example/v1','fixture',1,1,?,?)`, f.users[0], now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	response := f.call(0, "PUT", fmt.Sprintf("/api/endpoints/%d/request-adaptation", id), map[string]any{
		"expected_revision": "0", "fixed_headers": map[string]any{"mode": "replace", "values": map[string]any{"X-Private": map[string]any{"action": "replace", "value": "private-adaptation-sentinel"}}},
	}, false)
	if response.Code != 200 {
		t.Fatalf("adaptation setup: %d %s", response.Code, response.Body.String())
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := f.app.authRuntime.IdentityContinuity().UserKeyTx(context.Background(), tx, f.users[0])
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO identity_continuity_facts(identity_key,kind,scope,window_key,fact_json,occurred_at,expires_at) VALUES(?,'welfare','general','fixture-window','{}',?,?)`, identity[:], now, now+3600); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	document := readPersonalExport(t, f, 0)
	if len(document.RequestAdaptations) != 1 || len(document.Continuity) != 1 || document.Continuity[0].State != "completed" || !document.RequestAdaptations[0].FixedHeaders[0].HasValue {
		t.Fatalf("missing interaction projections: %+v", document.InteractionExport)
	}
	raw, _ := json.Marshal(document.InteractionExport)
	if strings.Contains(string(raw), "private-adaptation-sentinel") || strings.Contains(string(raw), hex.EncodeToString(identity[:])) {
		t.Fatal("sensitive adaptation or identity in export")
	}
	other := readPersonalExport(t, f, 1)
	if len(other.RequestAdaptations) != 0 || len(other.Continuity) != 0 {
		t.Fatal("interaction export crossed owner boundary")
	}
}
