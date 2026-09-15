package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	bidengine "github.com/waiting-here/NonbiriAPI/internal/game/bidding/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/compat"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type duelWireFixture struct {
	gameWireFixture
	t           *testing.T
	clock       *atomic.Int64
	users       [2]int64
	userCookies [2]*http.Cookie
	bindings    [2]string
	sequence    atomic.Int64
}

func newDuelWireFixture(t *testing.T) *duelWireFixture {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(time.Now().Unix())
	f := &duelWireFixture{gameWireFixture: newGameWireFixtureWithClock(t, func() time.Time { return time.Unix(clock.Load(), 0) }), t: t, clock: clock}
	f.users[0], f.userCookies[0] = f.userID, f.cookies[0]
	z := db.EncodeU128(db.U128{})
	r, err := f.store.DB().Exec(`INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "duel-player-two", "Second player", z, z, z, z, z, z, z, z, f.now, f.now)
	if err != nil {
		t.Fatal(err)
	}
	f.users[1], err = r.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,?)`, f.users[1], f.now); err != nil {
		t.Fatal(err)
	}
	f.userCookies[1] = &http.Cookie{Name: auth.UserSessionCookieName, Value: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{71}, 32))}
	for seat := range 2 {
		initialRevision, _ := db.ParseU128Decimal("1")
		if _, err := f.store.DB().Exec(`UPDATE users SET revision=? WHERE id=?`, db.EncodeU128(initialRevision), f.users[seat]); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(f.userCookies[seat].Value))
		f.bindings[seat] = hex.EncodeToString(digest[:])
		if seat == 1 {
			if _, err := f.store.DB().Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, f.bindings[seat], f.users[seat], f.now, f.now+3600, f.now+7200, f.now, "duel-session-generation"); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := f.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
			wallet, err := ledger.CreateUserAssetAccount(context.Background(), tx, f.users[seat], asset, f.now)
			if err != nil {
				t.Fatal(err)
			}
			if seat == 0 && asset == ledger.General {
				continue
			}
			external, err := ledger.CodedAssetAccount(context.Background(), tx, "external", asset)
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
				plan, err = ledger.NewAdminGameAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(int64(4-seat)*1000), "duel funding")
			} else {
				plan, err = ledger.NewAdminUserAdjustment(meta, wallet.ID, external.ID, ledger.AmountFromMilli(1000000), 0, ledger.Amount{}, "duel funding")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ledger.Apply(context.Background(), tx, plan); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	m := map[string]any{"enabled": true, "ticket": "10", "rake_bp": map[string]int{"platform": 275, "welfare": 100, "thursday": 50}}
	f.admin("PATCH", "/admin/api/games/config", map[string]any{"expected_revision": "2", "bidding": map[string]any{"enabled": true, "modes": map[string]any{"tier1": m, "tier2": m, "tier3": m}}, "likes": map[string]any{"enabled": true, "modes": map[string]any{"quick": m, "standard": m}}}, 200)
	return f
}
func wireBody(t *testing.T, v any) string {
	t.Helper()
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func (f *duelWireFixture) call(seat int, method, path string, body any, fresh bool) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, "http://duel.invalid"+path, strings.NewReader(wireBody(f.t, body)))
	req.Host = auditUserHost
	req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4242", 70+seat)
	req.AddCookie(f.userCookies[seat])
	req.Header.Set("Origin", "http://"+auditUserHost)
	req.Header.Set("Content-Type", "application/json")
	if method != "GET" {
		req.Header.Set("Idempotency-Key", fmt.Sprintf("%022d", f.sequence.Add(1)))
	}
	if fresh {
		token, _, err := f.app.authRuntime.ElevationManager().IssueBound(f.users[seat], elevation.KindUser, f.bindings[seat])
		if err != nil {
			f.t.Fatal(err)
		}
		req.Header.Set("X-Elevated-Token", token)
	}
	r := httptest.NewRecorder()
	f.app.handler.ServeHTTP(&scopeRecorder{r}, req)
	return r
}
func (f *duelWireFixture) admin(method, path string, body any, want int) *httptest.ResponseRecorder {
	f.t.Helper()
	r := testApplicationRequest(f.t, f.app.handler, method, auditAdminHost, path, wireBody(f.t, body), f.adminCookies, map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": fmt.Sprintf("%022d", f.sequence.Add(1))})
	if r.Code != want {
		f.t.Fatalf("%s %s: %d %s", method, path, r.Code, r.Body.String())
	}
	return r
}
func (f *duelWireFixture) read(seat int, game string) duel.Home {
	f.t.Helper()
	r := f.call(seat, "GET", "/api/games/"+game+"/state", nil, false)
	var h duel.Home
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &h) != nil {
		f.t.Fatalf("state: %d %s", r.Code, r.Body.String())
	}
	return h
}
func (f *duelWireFixture) enqueue(seat int, game, mode string) {
	f.t.Helper()
	r := f.call(seat, "GET", "/api/games", nil, false)
	var config compat.GamesSnapshot
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &config) != nil {
		f.t.Fatalf("snapshot: %d %s", r.Code, r.Body.String())
	}
	m := config.Bidding.Modes[mode]
	if game == "likes" {
		m = config.Likes.Modes[mode]
	}
	b := map[string]any{"mode": mode, "expected_terms_hash": m.TermsHash, "device_token": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(1 + seat)}, 32))}
	if game == "likes" {
		b["loadout"] = map[string]any{"role": "ChatGPT", "skills": []string{"PUB01", "GPT44"}, "harness": nil}
	}
	r = f.call(seat, "POST", "/api/games/"+game+"/queue", b, false)
	if r.Code != 202 {
		f.t.Fatalf("enqueue: %d %s", r.Code, r.Body.String())
	}
}
func (f *duelWireFixture) matched(game, mode string) duel.State {
	f.t.Helper()
	f.enqueue(0, game, mode)
	f.enqueue(1, game, mode)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h := f.read(0, game)
		if h.Current != nil {
			return *h.Current
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatal("match worker did not pair both players")
	return duel.State{}
}
func (f *duelWireFixture) action(seat int, s duel.State, action any) {
	f.t.Helper()
	r := f.call(seat, "POST", "/api/games/"+s.Game+"/sessions/"+s.ID+"/actions", map[string]any{"phase_seq": s.PhaseSeq, "action": action}, false)
	if r.Code != 200 {
		f.t.Fatalf("action %s %d: %d %s", s.Game, s.Round, r.Code, r.Body.String())
	}
}
func (f *duelWireFixture) checkLedger() {
	f.t.Helper()
	ctx := context.Background()
	if err := f.app.games.ValidatePersistedState(ctx); err != nil {
		f.t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback()
	if err = ledger.ValidateRecovery(ctx, tx); err != nil {
		f.t.Fatal(err)
	}
}

func TestDuelPeriodicRecoveryPreservesAcceptedGames(t *testing.T) {
	f := newDuelWireFixture(t)
	bid := f.matched("bidding", "tier1")
	likes := f.matched("likes", "quick")
	for seat := range 2 {
		f.action(seat, likes, map[string]any{"kind": "plan", "plan": map[string]any{"purchases": []any{}, "main": map[string]string{"skillId": "PUB01"}, "extra": []any{}}})
	}
	for range 2 {
		for _, game := range []string{"bidding", "likes"} {
			if _, err := f.app.games.RecoverModule(context.Background(), game, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if h := f.read(0, "bidding"); h.Current == nil || h.Current.ID != bid.ID || h.LatestResult != nil {
		t.Fatal("periodic recovery cancelled bidding", h)
	}
	if h := f.read(0, "likes"); h.Current == nil || h.Current.ID != likes.ID || h.Current.Phase != "settlement" || h.LatestResult != nil {
		t.Fatal("periodic recovery cancelled settlement", h)
	}
	f.clock.Add(5)
	if _, err := f.app.games.RecoverModule(context.Background(), "likes", f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if h := f.read(0, "likes"); h.Current == nil || h.Current.Round != 2 || h.Current.Phase != "plan" || h.Current.Deadline == nil || *h.Current.Deadline-f.clock.Load() != 20 {
		t.Fatal("periodic recovery did not advance normally", h)
	}
	f.checkLedger()
}

func (f *duelWireFixture) userRevision(seat int) string {
	f.t.Helper()
	var raw []byte
	if err := f.store.DB().QueryRow(`SELECT revision FROM users WHERE id=?`, f.users[seat]).Scan(&raw); err != nil {
		f.t.Fatal(err)
	}
	v, err := db.DecodeU128(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	return v.Decimal()
}
func (f *duelWireFixture) settlement() duel.State {
	f.t.Helper()
	s := f.matched("likes", "quick")
	a := map[string]any{"kind": "plan", "plan": map[string]any{"purchases": []any{}, "main": map[string]string{"skillId": "PUB01"}, "extra": []any{}}}
	for seat := range 2 {
		f.action(seat, s, a)
	}
	s = *f.read(0, "likes").Current
	if s.Phase != "settlement" {
		f.t.Fatal("expected settlement")
	}
	return s
}
func (f *duelWireFixture) assertCancelled(game, id, reason string) {
	f.t.Helper()
	h := f.read(1, game)
	if h.Current != nil || h.LatestResult == nil || h.LatestResult.ID != id || h.LatestResult.Outcome != "system_cancelled" || h.LatestResult.Reason != reason || h.LatestResult.OwnPayment != h.LatestResult.OwnRefund || h.LatestResult.PrizeGeneral != "0" {
		f.t.Fatalf("cancelled %s: %+v", game, h)
	}
}

func TestDuelProductionCancellationEntrypointsAreAtomic(t *testing.T) {
	for _, kind := range []string{"administrator", "steward", "automatic", "deletion", "restart", "ban rollback"} {
		t.Run(kind, func(t *testing.T) {
			f := newDuelWireFixture(t)
			bid := f.matched("bidding", "tier1")
			likes := f.settlement()
			openings := map[string]string{"bidding": readRandomProof(f, 1, "bidding", bid.ID).Commitment, "likes": readRandomProof(f, 1, "likes", likes.ID).Commitment}
			reason := "account_unavailable"
			switch kind {
			case "administrator", "ban rollback":
				body := map[string]any{"expected_revision": f.userRevision(0), "reason": "Account restriction", "duration_seconds": 60}
				if kind == "ban rollback" {
					if _, err := f.store.DB().Exec(`CREATE TRIGGER reject_duel_ban BEFORE UPDATE OF is_banned ON users WHEN NEW.is_banned=1 BEGIN SELECT RAISE(ABORT,'ban write rejected'); END`); err != nil {
						t.Fatal(err)
					}
					r := f.admin("POST", fmt.Sprintf("/admin/api/users/%d/ban", f.users[0]), body, 500)
					_ = r
					if f.read(1, "likes").Current == nil || f.read(1, "bidding").Current == nil {
						t.Fatal("refund escaped failed ban transaction")
					}
					var terminals int
					if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='duel_terminal'`).Scan(&terminals); err != nil || terminals != 0 {
						t.Fatal("terminal postings escaped rollback", terminals, err)
					}
					if _, err := f.store.DB().Exec(`DROP TRIGGER reject_duel_ban`); err != nil {
						t.Fatal(err)
					}
				}
				f.admin("POST", fmt.Sprintf("/admin/api/users/%d/ban", f.users[0]), body, 204)
			case "steward":
				if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, f.users[1]); err != nil {
					t.Fatal(err)
				}
				r := f.call(1, "POST", fmt.Sprintf("/api/steward/users/%d/ban", f.users[0]), map[string]any{"expected_revision": f.userRevision(0), "reason": "Account restriction", "duration_seconds": 60}, false)
				if r.Code != 204 {
					t.Fatalf("steward ban: %d %s", r.Code, r.Body.String())
				}
			case "automatic":
				if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key=?`, antiabuse.KeyRPMBanThreshold); err != nil {
					t.Fatal(err)
				}
				f.app.forward.abuse.RPMDenied(context.Background(), f.users[0], ratelimit.RPMUserLimit)
				var banned int
				if err := f.store.DB().QueryRow(`SELECT is_banned FROM users WHERE id=?`, f.users[0]).Scan(&banned); err != nil || banned != 1 {
					t.Fatal("automatic ban missing", err)
				}
			case "deletion":
				r := f.call(0, "POST", "/api/account/delete", map[string]string{"confirm": "DELETE"}, true)
				if r.Code != 204 {
					t.Fatalf("delete: %d %s", r.Code, r.Body.String())
				}
				var wallets int
				if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE user_id=?`, f.users[0]).Scan(&wallets); err != nil || wallets != 0 {
					t.Fatal("wallet survived delete", err)
				}
				late := f.call(0, "POST", "/api/games/likes/sessions/"+likes.ID+"/actions", map[string]any{"phase_seq": likes.PhaseSeq, "action": map[string]string{"kind": "plan"}}, false)
				if late.Code != 401 && late.Code != 403 {
					t.Fatalf("late request: %d", late.Code)
				}
			case "restart":
				if err := f.app.Close(); err != nil {
					t.Fatal(err)
				}
				vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = vault.Close() })
				f.app, err = buildApplicationWithGameClock(auditConfig(), f.store, vault, func() time.Time { return time.Unix(f.clock.Load(), 0) })
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = f.app.Close() })
				reason = "server_restart"
			}
			f.assertCancelled("bidding", bid.ID, reason)
			f.assertCancelled("likes", likes.ID, reason)
			for game, id := range map[string]string{"bidding": bid.ID, "likes": likes.ID} {
				proof := readRandomProof(f, 1, game, id)
				if proof.Commitment != openings[game] || len(proof.Seed) != 64 {
					t.Fatal("cancellation lost or changed opening", game)
				}
			}
			f.clock.Add(10)
			f.assertCancelled("likes", likes.ID, reason)
			f.checkLedger()
		})
	}
}

func TestDuelProductionMaintenanceAllowsSettlementAndManualPlanIsRequired(t *testing.T) {
	f := newDuelWireFixture(t)
	s := f.matched("likes", "quick")
	r := f.call(0, "POST", "/api/games/likes/sessions/"+s.ID+"/actions", map[string]any{"phase_seq": s.PhaseSeq, "action": map[string]any{"kind": "plan", "plan": map[string]any{"purchases": []any{}, "main": nil, "extra": []any{}}}}, false)
	if r.Code != 400 || f.read(0, "likes").Current.Locked[s.You] {
		t.Fatal("manual empty action was accepted")
	}
	a := map[string]any{"kind": "plan", "plan": map[string]any{"purchases": []any{}, "main": map[string]string{"skillId": "PUB01"}, "extra": []any{}}}
	for seat := range 2 {
		f.action(seat, s, a)
	}
	s = *f.read(0, "likes").Current
	f.admin("POST", "/admin/api/maintenance/enable", map[string]any{"expected_revision": "2", "reason": "Maintenance validation", "confirmation": true}, 200)
	for seat := range 2 {
		if h := f.read(seat, "likes"); h.Current == nil || h.Current.Phase != "settlement" {
			t.Fatal("maintenance lost settlement")
		}
		for _, path := range []string{"/api/games/likes/catalog", "/api/games/likes/sessions/" + s.ID + "/rounds", "/api/games/likes/randomness/" + s.ID} {
			r := f.call(seat, "GET", path, nil, false)
			if r.Code != 200 {
				t.Fatalf("continuation %s: %d %s", path, r.Code, r.Body.String())
			}
		}
	}
	f.clock.Store(*s.Deadline)
	s = *f.read(0, "likes").Current
	if s.Phase != "plan" || *s.Deadline != f.clock.Load()+20 {
		t.Fatal("maintenance shortened plan")
	}
	for seat := range 2 {
		f.action(seat, s, a)
	}
	s = *f.read(0, "likes").Current
	r = f.call(0, "POST", "/api/games/likes/sessions/"+s.ID+"/surrender", map[string]string{"phase_seq": s.PhaseSeq}, false)
	if r.Code != 200 {
		t.Fatalf("maintenance surrender: %d %s", r.Code, r.Body.String())
	}
	if r := f.read(1, "likes").LatestResult; r == nil || r.Outcome != "win" {
		t.Fatal("maintenance result missing")
	}
	f.checkLedger()
}

func TestDuelProductionFullMatchesAndSettlementProjection(t *testing.T) {
	for _, gameMode := range [][2]string{{"bidding", "tier1"}, {"likes", "quick"}, {"likes", "standard"}} {
		t.Run(gameMode[0]+"/"+gameMode[1], func(t *testing.T) {
			f := newDuelWireFixture(t)
			g, mode := gameMode[0], gameMode[1]
			s := f.matched(g, mode)
			id := s.ID
			rounds := 13
			if g == "likes" {
				rounds = 25
				if mode == "standard" {
					rounds = 75
				}
			}
			for round := 1; round <= rounds; round++ {
				if s.Round != round {
					t.Fatalf("round=%d, want=%d", s.Round, round)
				}
				if s.Phase == "joker" {
					var v bidengine.View
					if json.Unmarshal(s.View, &v) != nil || v.Dealer == nil {
						t.Fatal("dealer projection")
					}
					player := 0
					if s.You != *v.Dealer {
						player = 1
					}
					f.action(player, s, map[string]any{"kind": "joker", "use": true})
					s = *f.read(0, g).Current
				}
				for seat := range 2 {
					if g == "bidding" {
						card := round
						if seat == 1 {
							card = 14 - round
						}
						f.action(seat, s, map[string]any{"kind": "bid", "card": card})
					} else {
						skill := "GPT44"
						if round <= 3 {
							skill = "PUB01"
						}
						f.action(seat, s, map[string]any{"kind": "plan", "plan": map[string]any{"purchases": []any{}, "main": map[string]string{"skillId": skill}, "extra": []any{}}})
					}
				}
				h := f.read(0, g)
				if g == "likes" {
					var resolution *duel.Resolution
					if h.Current != nil {
						resolution = h.Current.Resolution
						if h.Current.Phase != "settlement" {
							t.Fatal("missing settlement")
						}
					} else if h.LatestResult != nil {
						resolution = h.LatestResult.Resolution
					}
					if resolution == nil || resolution.Round != round || resolution.EndsAt-resolution.StartedAt != 5 {
						t.Fatal("presentation timing", resolution)
					}
					other := f.read(1, g)
					var otherResolution *duel.Resolution
					if other.Current != nil {
						otherResolution = other.Current.Resolution
					} else {
						otherResolution = other.LatestResult.Resolution
					}
					if wireBody(t, resolution) != wireBody(t, otherResolution) {
						t.Fatal("different simultaneous presentation")
					}
					f.clock.Store(resolution.EndsAt - 1)
					if round < rounds && f.read(1, g).Current.Phase != "settlement" {
						t.Fatal("presentation ended early")
					}
					f.clock.Store(resolution.EndsAt)
					h = f.read(0, g)
				}
				if round < rounds {
					if h.Current == nil {
						t.Fatalf("early terminal: %+v", h.LatestResult)
					}
					s = *h.Current
					if g == "likes" && (s.Phase != "plan" || *s.Deadline != f.clock.Load()+20) {
						t.Fatal("next round shortened")
					}
					if g == "bidding" {
						f.clock.Add(1)
					}
				} else if h.Current != nil || h.LatestResult == nil {
					t.Fatal("missing final result")
				}
			}
			for seat := range 2 {
				h := f.read(seat, g)
				r := h.LatestResult
				if r == nil || r.ID != id {
					t.Fatal("terminal identity")
				}
				if r.Outcome == "win" && (r.PrizeGeneral != "9.575" || r.OwnRefund != r.OwnPayment) {
					t.Fatalf("winner settlement: %+v", r)
				}
				if r.Outcome == "draw" && (r.PrizeGeneral != "0" || r.OwnRefund != r.OwnPayment) {
					t.Fatalf("draw settlement: %+v", r)
				}
				exported := f.call(seat, "POST", "/api/account/export", nil, true)
				var doc lifecycle.ExportDocument
				if exported.Code != 200 || json.Unmarshal(exported.Body.Bytes(), &doc) != nil || doc.SchemaVersion != 8 {
					t.Fatalf("personal export: %d %s", exported.Code, exported.Body.String())
				}
				v := doc.Bidding
				if g == "likes" {
					v = doc.Likes
				}
				if len(v.History) != 1 || len(v.History[0].Rounds) != rounds {
					t.Fatal("incomplete exported rounds")
				}
				private := wireBody(t, v)
				if strings.Contains(private, "profiles") || strings.Contains(private, "display_name") || strings.Contains(private, "user_id") {
					t.Fatal("identity leaked into personal game export")
				}
			}
			f.admin("GET", "/admin/api/games/"+g+"/history?dataset=recent", nil, 200)
			f.admin("POST", "/admin/api/games/"+g+"/history/export", map[string]any{"dataset": "recent", "selection": map[string]any{}, "cursor": nil}, 200)
			f.checkLedger()
		})
	}
}
