package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestGameOnboardingHTTPExportAndPhysicalDeletion(t *testing.T) {
	f := newGameWireFixture(t)
	ctx := context.Background()
	users := [3]int64{f.userID}
	cookies := [3]*http.Cookie{f.cookies[0]}
	bindings := [3]string{}
	zero := db.EncodeU128(db.U128{})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if i > 0 {
			result, err := f.store.DB().Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
				fmt.Sprintf("game-export-%d", i), fmt.Sprintf("game player %d", i), zero, zero, zero, zero, zero, zero, zero, zero, f.now, f.now)
			if err != nil {
				t.Fatal(err)
			}
			users[i], err = result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			raw := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(70 + i)}, 32))
			cookies[i] = &http.Cookie{Name: auth.UserSessionCookieName, Value: raw}
		}
		hash := sha256.Sum256([]byte(cookies[i].Value))
		bindings[i] = hex.EncodeToString(hash[:])
		if i > 0 {
			exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, bindings[i], users[i], f.now, f.now+3600, f.now+7200, f.now, "game-export-session")
		}
		tx, err := f.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			wallet, err := ledger.CreateUserAssetAccount(ctx, tx, users[i], asset, f.now)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 && asset == ledger.General {
				continue
			}
			external, err := ledger.CodedAssetAccount(ctx, tx, "external", asset)
			if err != nil {
				t.Fatal(err)
			}
			id, err := db.GenerateOpaqueID("op_")
			if err != nil {
				t.Fatal(err)
			}
			meta := ledger.Meta{OperationID: id, ActorUserID: f.adminID, CreatedAt: f.now}
			var plan ledger.Plan
			if asset == ledger.Game {
				plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(600), "game entry fixture")
			} else {
				plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(2000), 0, ledger.Amount{}, "game entry fixture")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ledger.Apply(ctx, tx, plan); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE site_config SET value='1' WHERE key=?", rpsconfig.RPSEnabledKey)
	for _, mode := range []string{game.RPSModeQuick, game.RPSModeStandard} {
		exec("UPDATE site_config SET value='1' WHERE key=?", rpsconfig.RPSModeEnabledKey(mode))
		exec("UPDATE site_config SET value='1000' WHERE key=?", rpsconfig.RPSModeBaseKey(mode))
	}
	requestSeq := 0
	call := func(player int, method, path, body string, elevated bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://wire.invalid"+path, strings.NewReader(body))
		req.Host = auditUserHost
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4242", 30+player)
		req.AddCookie(cookies[player])
		req.Header.Set("Origin", "http://"+auditUserHost)
		req.Header.Set("Content-Type", "application/json")
		requestSeq++
		req.Header.Set("Idempotency-Key", fmt.Sprintf("%022d", requestSeq))
		if elevated {
			token, _, err := f.app.authRuntime.ElevationManager().IssueBound(users[player], elevation.KindUser, bindings[player])
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Elevated-Token", token)
		}
		response := httptest.NewRecorder()
		f.app.handler.ServeHTTP(&scopeRecorder{response}, req)
		return response
	}
	queue := func(mode string) {
		t.Helper()
		for i := range users {
			body := fmt.Sprintf(`{"mode":%q,"device_token":%q,"deathmatch_confirmed":false}`, mode, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(90 + i)}, 32)))
			response := call(i, "POST", rps.RouteQueue, body, false)
			if response.Code != http.StatusAccepted {
				t.Fatalf("queue: %d %s", response.Code, response.Body.String())
			}
			var payment struct {
				RulesVersion int          `json:"rules_version"`
				Payment      game.Payment `json:"payment"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payment); err != nil {
				t.Fatal(err)
			}
			if payment.RulesVersion != 2 || mode == game.RPSModeQuick && payment.Payment != (game.Payment{General: "0.4", Game: "0.6"}) {
				t.Fatal("new HTTP payment facts", payment)
			}
		}
		service := f.app.games.AccountContinuation().(*rps.Service)
		// The running worker may already have matched these players. Verify
		// the resulting game through the HTTP states and ledger below.
		if _, err := service.MatchOnce(ctx, mode); err != nil {
			t.Fatal(err)
		}
	}
	state := func(player int) rps.HomeState {
		t.Helper()
		response := call(player, "GET", rps.RouteState, "", false)
		var value rps.HomeState
		if response.Code != 200 {
			t.Fatalf("state: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	queue(game.RPSModeQuick)
	sessionID := ""
	for i := range users {
		current := state(i)
		if current.Session == nil {
			t.Fatal("new HTTP entry did not select current rules")
		}
		sessionID = current.Session.SessionID
		body := fmt.Sprintf(`{"phase_seq":%q,"expected_revision":%q,"action":"gesture","payload":{"gesture":"rock"}}`, current.Session.PhaseSeq, current.Session.Revision)
		response := call(i, "POST", "/api/games/rps/sessions/"+sessionID+"/actions", body, false)
		if response.Code != 200 {
			t.Fatalf("gesture: %d %s", response.Code, response.Body.String())
		}
	}
	progress := call(0, "GET", "/api/games", "", false)
	var snapshot struct {
		Onboarding map[string]game.OnboardingProgress `json:"onboarding"`
	}
	if progress.Code != 200 {
		t.Fatalf("progress: %d %s", progress.Code, progress.Body.String())
	}
	if err := json.Unmarshal(progress.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	items := snapshot.Onboarding["rps"].Items
	if len(items) != 3 || !items[0].Completed || items[0].Reward != "1000" || items[1].Completed {
		t.Fatal("HTTP reward progress", items)
	}
	exported := call(0, "POST", "/api/account/export", "", true)
	var document lifecycle.ExportDocument
	if exported.Code != 200 {
		t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
	}
	if err := json.Unmarshal(exported.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 6 || len(document.GameOnboarding) != 1 || document.GameOnboarding[0].GameKey != "rps" || document.GameOnboarding[0].TaskKey != "quick" || document.GameOnboarding[0].Award != "1000" || document.User.GameBalance != "0" {
		t.Fatalf("earned reward missing from export: %+v", document.GameOnboarding)
	}
	for i := range users {
		response := call(i, "POST", rps.RoutePendingACK, fmt.Sprintf(`{"session_id":%q}`, sessionID), false)
		if response.Code != 204 {
			t.Fatalf("ack: %d %s", response.Code, response.Body.String())
		}
	}
	queue(game.RPSModeStandard)
	ongoing := state(0)
	if ongoing.Session == nil {
		t.Fatal("standard queue did not produce a live session")
	}
	deleted := call(0, "POST", "/api/account/delete", `{"confirm":"DELETE"}`, true)
	if deleted.Code != 204 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	for _, table := range []string{"game_onboarding_completions", "game_onboarding_holds", "credit_accounts", "sessions"} {
		var count int
		if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM "+table+" WHERE user_id=?", users[0]).Scan(&count); err != nil || count != 0 {
			t.Fatal("private data retained", table, count, err)
		}
	}
	var count int
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM users WHERE id=?", users[0]).Scan(&count); err != nil || count != 0 {
		t.Fatal("user retained", count, err)
	}
	// The live match still contains two surviving players. The deleted
	// player's late HTTP action cannot recreate a wallet or reward.
	stale := call(0, "POST", "/api/games/rps/sessions/"+ongoing.Session.SessionID+"/actions", fmt.Sprintf(`{"phase_seq":%q,"expected_revision":%q,"action":"gesture","payload":{"gesture":"rock"}}`, ongoing.Session.PhaseSeq, ongoing.Session.Revision), false)
	if stale.Code != 401 {
		t.Fatalf("late action: %d %s", stale.Code, stale.Body.String())
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM game_onboarding_holds").Scan(&count); err != nil || count != 2 {
		t.Fatal("survivor holds", count, err)
	}

	// Recover each due phase using the persisted deadline. This exercises
	// late authoritative callbacks after the account has physically gone.
	service := f.app.games.AccountContinuation().(*rps.Service)
	for step := 0; ; step++ {
		var deadline int64
		err := f.store.DB().QueryRow("SELECT COALESCE(phase_deadline,terminal_next_retry_at) FROM game_rps_sessions WHERE id=?", ongoing.Session.SessionID).Scan(&deadline)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil || step >= 128 {
			t.Fatal("remaining match did not converge", step, err)
		}
		if _, err := service.RecoverBeforeListenAt(ctx, deadline, 20, time.Now().Add(5*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM game_onboarding_holds").Scan(&count); err != nil || count != 0 {
		t.Fatal("terminal holds", count, err)
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM game_onboarding_completions").Scan(&count); err != nil || count != 4 {
		t.Fatal("survivor qualifications", count, err)
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'").Scan(&count); err != nil || count != 5 {
		t.Fatal("late rewards", count, err)
	}
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM credit_accounts WHERE user_id=?", users[0]).Scan(&count); err != nil || count != 0 {
		t.Fatal("late callback recreated wallet", count, err)
	}
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = ledger.ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if len(document.RPS.Summaries) == 0 {
		t.Fatal("ordinary game summary missing")
	}
	if document.User.ID != strconv.FormatInt(users[0], 10) {
		t.Fatal("wrong export subject")
	}
}
