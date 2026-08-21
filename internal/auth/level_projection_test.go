package auth

// /api/session and /api/me additive level/economy projection tests: the wire
// carries canonical decimal-string balances plus the resolved effective level
// and the nullable manual level, with the manual-null and manual-set shapes
// both pinned.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func sessionGet(t *testing.T, service *UserAuth, st *db.Store, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := st.CreateUserSession(userID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	r := stationRequest(http.MethodGet, "https://example.com/api/session", host.StationUser, nil)
	r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.Session(rec, r)
	return rec
}

func TestSessionProjectsLevelAndEconomyWire(t *testing.T) {
	st := authTestStore(t)
	service := newTestUserAuth(t, st, &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-level-1", Username: "alice"},
	}}, nil)
	user, err := st.CreateUser("discord-level-1", "alice", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Seed economy state and an enabled threshold that the balance qualifies
	// for, so the lazy promotion is observable through the wire.
	if err := st.SetSiteConfigValue(db.LevelThreshold2Key, "100000"); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET credits=1234567, donation_credit=100000 WHERE id=?`, user.ID); err != nil {
		t.Fatalf("seed balances: %v", err)
	}

	rec := sessionGet(t, service, st, user.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		User struct {
			Credits        string `json:"credits"`
			DonationCredit string `json:"donation_credit"`
			EffectiveLevel int    `json:"effective_level"`
			ManualLevel    *int   `json:"manual_level"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v body=%s", err, rec.Body.Bytes())
	}
	u := envelope.User
	if u.Credits != "1234567" || u.DonationCredit != "100000" {
		t.Fatalf("balances = (%q, %q), want canonical strings", u.Credits, u.DonationCredit)
	}
	if u.EffectiveLevel != 2 {
		t.Fatalf("effective level = %d, want 2 (lazily promoted, persisted)", u.EffectiveLevel)
	}
	if u.ManualLevel != nil {
		t.Fatalf("manual level = %+v, want null", u.ManualLevel)
	}
	// The lazy promotion really persisted.
	var auto int
	if err := st.DB().QueryRow(`SELECT auto_level FROM users WHERE id=?`, user.ID).Scan(&auto); err != nil || auto != 2 {
		t.Fatalf("persisted auto_level = (%d, %v), want 2", auto, err)
	}

	// A manual override projects as a non-null manual_level and wins on the
	// wire without touching the high-water mark.
	five := 5
	if _, err := st.SetUserManualLevel(user.ID, &five); err != nil {
		t.Fatalf("set manual: %v", err)
	}
	rec = sessionGet(t, service, st, user.ID)
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("second envelope: %v", err)
	}
	if envelope.User.EffectiveLevel != 5 || envelope.User.ManualLevel == nil || *envelope.User.ManualLevel != 5 {
		t.Fatalf("manual projection = (%d, %+v), want (5, 5)", envelope.User.EffectiveLevel, envelope.User.ManualLevel)
	}
	if err := st.DB().QueryRow(`SELECT auto_level FROM users WHERE id=?`, user.ID).Scan(&auto); err != nil || auto != 2 {
		t.Fatalf("auto_level after manual = (%d, %v), want unchanged 2", auto, err)
	}
}

func TestSessionNegativeBalanceIsCanonicalString(t *testing.T) {
	st := authTestStore(t)
	service := newTestUserAuth(t, st, &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-level-2", Username: "bob"},
	}}, nil)
	user, err := st.CreateUser("discord-level-2", "bob", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET credits=-75 WHERE id=?`, user.ID); err != nil {
		t.Fatalf("seed negative balance: %v", err)
	}

	rec := sessionGet(t, service, st, user.ID)
	var envelope struct {
		User struct {
			Credits        string `json:"credits"`
			DonationCredit string `json:"donation_credit"`
			EffectiveLevel int    `json:"effective_level"`
			ManualLevel    *int   `json:"manual_level"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v body=%s", err, rec.Body.String())
	}
	if envelope.User.Credits != "-75" {
		t.Fatalf("negative balance = %q, want \"-75\"", envelope.User.Credits)
	}
	if envelope.User.DonationCredit != "0" || envelope.User.EffectiveLevel != 1 || envelope.User.ManualLevel != nil {
		t.Fatalf("neutral projection = %+v", envelope.User)
	}
}
