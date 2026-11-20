package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

type fixtureAuth struct{ deny bool }

func (a *fixtureAuth) AuthorizeAdminMutation(context.Context, *sql.Tx) error {
	if a.deny {
		return authz.ErrForbidden
	}
	return nil
}

func TestAdminEditsReplayAndBoundedEpochs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE whale_haven_settings(revision INTEGER PRIMARY KEY,effective_at INTEGER NOT NULL,document TEXT NOT NULL);
 CREATE INDEX settings_time ON whale_haven_settings(effective_at,revision);
 CREATE TABLE whale_haven_master_history(effective_at INTEGER PRIMARY KEY,enabled INTEGER);
 INSERT INTO whale_haven_master_history VALUES (0,0),(2000,1),(5000,0);
 CREATE TABLE users(id INTEGER PRIMARY KEY,is_admin INTEGER);
 INSERT INTO users VALUES(42,1);
 CREATE TABLE idempotency_records(scope TEXT,actor_scope_hash BLOB,key_hash BLOB,request_hash BLOB,state TEXT,http_status INTEGER,response_body BLOB,created_at INTEGER,expires_at INTEGER,PRIMARY KEY(scope,actor_scope_hash,key_hash));`)
	if err != nil {
		t.Fatal(err)
	}
	at := int64(2000)
	auth := &fixtureAuth{}
	api := adminAPI{db, db, auth, func() time.Time { return time.UnixMilli(at) }}
	const firstKey = "abcdefghijklmnopqrstuv"
	const nextKey = "bbcdefghijklmnopqrstuv"
	const thirdKey = "cbcdefghijklmnopqrstuv"
	const first = `{"expected_revision":"1","enabled":true,"values":{"welfare_cut_bp":0,"daily_exchange_pearls":150,"wallet_cap_milli":500000000}}`
	call := func(key, body string, code int) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, RouteAdminConfig, strings.NewReader(body))
		req.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		api.patch(response, req)
		if response.Code != code {
			t.Fatalf("PATCH %s: got %d, want %d: %s", body, response.Code, code, response.Body.String())
		}
		return response.Body.String()
	}
	auth.deny = true
	call(firstKey, first, 403)
	auth.deny = false
	applied := call(firstKey, first, 200)
	var response struct {
		Settings
		Revision string
		Fields   []Field
	}
	if err := json.Unmarshal([]byte(applied), &response); err != nil {
		t.Fatal(err)
	}
	if response.Revision != "2" || !response.Enabled || response.ExchangeEnabled || response.Value("welfare_cut_bp") != 0 || response.Value("lot_gross_milli") != 5000000 || len(response.Fields) != len(fields) {
		t.Fatal("bad first response")
	}
	call(nextKey, first, 409)
	call(firstKey, `{"expected_revision":"2","enabled":false}`, 409)
	call(nextKey, `{"expected_revision":"2","values":{"thursday_cut_bp":9700}}`, 400)
	call(nextKey, `{"expected_revision":"2","values":{"unknown_field":4}}`, 400)
	call(nextKey, `{"expected_revision":"2","enabled":null}`, 400)
	call(nextKey, `{"expected_revision":"2","values":{"platform_cut_bp":null}}`, 400)
	call(nextKey, `{"expected_revision":"2","values":{"platform_cut_bp":0,"platform_cut_bp":4}}`, 400)
	call(nextKey, `{"expected_revision":"2","exchange_enabled":true}`, 200)
	at = 4000
	call(thirdKey, `{"expected_revision":"3","values":{"platform_cut_bp":0,"thursday_cut_bp":0}}`, 200)
	if again := call(firstKey, first, 200); again != applied {
		t.Fatal("replay changed")
	}
	call("dbcdefghijklmnopqrstuv", `{"expected_revision":"4","enabled":true}`, 200)
	var n int
	if err := db.QueryRow("SELECT count(*) FROM whale_haven_settings").Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	timeline, err := History(t.Context(), tx, 1000, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) != 4 || timeline[0].Master || timeline[0].Settings.Enabled || !timeline[1].Master || !timeline[1].Settings.ExchangeEnabled || timeline[1].AtMS != 2000 || timeline[2].Settings.Value("platform_cut_bp") != 0 || timeline[3].Master {
		t.Fatalf("unexpected timeline %v", timeline)
	}
	tail, err := History(t.Context(), tx, 3000, 3999)
	if err != nil || len(tail) != 1 || tail[0].AtMS != 3000 || !tail[0].Master || tail[0].Settings.Value("platform_cut_bp") != 300 {
		t.Fatal(tail, err)
	}
}

func TestDefaultEconomy(t *testing.T) {
	s := Defaults()
	if s.Enabled || s.ExchangeEnabled {
		t.Fatal("enabled on initial install")
	}
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	if s.Value("daily_exchange_pearls") != 75 || s.Value("wallet_cap_milli") != 300000000 || s.Value("guest_wage_bp") != 2000 {
		t.Fatal("limits")
	}
	if s.Value("conversion_base_bp")+s.Value("mood_high_bonus_bp")+s.Value("affection_level_10_bp")+s.Value("sleep_bonus_bp")+s.Value("decor_jelly_lamp_bonus_bp")+s.Value("decor_coral_tea_bonus_bp") != 12000 {
		t.Fatal("best attainable conversion")
	}
	for _, f := range fields {
		if f.ZH == "" || f.EN == "" || f.Unit == "" || f.Key == "" {
			t.Fatalf("incomplete field %+v", f)
		}
	}
}
