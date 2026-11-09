package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/attendance"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
)

func TestAttendanceRoutesAutomaticAwardsExportAndDeletion(t *testing.T) {
	f := newActivityWireFixture(t)
	if _, err := f.store.DB().Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','480',0) ON CONFLICT(key) DO UPDATE SET value='480'; UPDATE site_config SET value='1' WHERE key='activities_enabled'"); err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("site", 480*60)
	tomorrow := f.now().In(zone).AddDate(0, 0, 1).Format(time.DateOnly)
	in := attendance.Input{Title: "A new day", Description: "Automatic rewards", StartDate: tomorrow, EndDate: tomorrow, Mode: "cumulative", Published: true, Tiers: []attendance.TierInput{{Days: 1, Rewards: []rewardasset.Item{{Asset: "general", Quantity: "1.125"}, {Asset: "game", Quantity: "2"}}}}}
	if r := f.request("POST", "/admin/api/attendance", in, f.users[0].Cookie, true); r.Code != http.StatusUnauthorized && r.Code != http.StatusForbidden {
		t.Fatal("user configured attendance", r.Code)
	}
	r := f.request("POST", "/admin/api/attendance", in, f.adminCookie, true)
	var p attendance.Period
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &p) != nil {
		t.Fatal(r.Code, r.Body.String())
	}
	if p.ID == "" || !p.ConfigEditable {
		t.Fatal(p)
	}
	var audited int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM management_operations WHERE domain='attendance' AND outcome='succeeded'").Scan(&audited); err != nil || audited != 1 {
		t.Fatal("configuration audit", audited, err)
	}
	start, err := time.ParseInLocation(time.DateOnly, tomorrow, zone)
	if err != nil {
		t.Fatal(err)
	}
	f.offset.Store(start.Unix() - time.Now().Unix() + 10)
	path := "/api/attendance/" + p.ID + "/sign-in"
	user := f.users[0]
	request := func(body, origin, key string) (int, []byte) {
		r := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, path, body, []*http.Cookie{user.Cookie}, map[string]string{"Origin": origin, "Content-Type": "application/json", "Idempotency-Key": key})
		return r.Code, r.Body.Bytes()
	}
	for _, body := range []string{`{"site_date":"` + tomorrow + `","user_id":1}`, `{"site_date":"` + tomorrow + `","site_date":"` + tomorrow + `"}`, `{"site_date":null}`} {
		if code, _ := request(body, "http://"+f.cfg.UserHost, "attendance-invalid-key-0001"); code != http.StatusBadRequest {
			t.Fatal("invalid input accepted", code)
		}
	}
	body := wireBody(t, attendance.SignInInput{SiteDate: tomorrow})
	if code, _ := request(body, "https://untrusted.example", "attendance-csrf-key-00001"); code != http.StatusForbidden {
		t.Fatal("CSRF accepted", code)
	}
	code, raw := request(body, "http://"+f.cfg.UserHost, "attendance-sign-in-key-0001")
	var result attendance.SignInResult
	if code != http.StatusOK || json.Unmarshal(raw, &result) != nil || result.AlreadySigned || len(result.Awards) != 1 || len(result.Awards[0].Results) != 2 {
		t.Fatal(code, string(raw))
	}
	code, raw = request(body, "http://"+f.cfg.UserHost, "attendance-sign-in-key-0001")
	if code != http.StatusOK || json.Unmarshal(raw, &result) != nil || result.AlreadySigned {
		t.Fatal("retry lost receipt", code, string(raw))
	}
	other := f.call("GET", "/api/attendance", nil, f.users[1].Cookie, false)["items"].([]any)
	if len(other) != 1 || other[0].(map[string]any)["total_days"] != float64(0) {
		t.Fatal("other account progress", other)
	}
	userID, err := strconv.ParseInt(user.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	binding := sha256.Sum256([]byte(user.Cookie.Value))
	account := func(path, body string) []byte {
		t.Helper()
		token, _, err := f.app.Load().authRuntime.ElevationManager().IssueBound(userID, elevation.KindUser, hex.EncodeToString(binding[:]))
		if err != nil {
			t.Fatal(err)
		}
		r := testApplicationRequest(t, f.app.Load().handler, "POST", f.cfg.UserHost, path, body, []*http.Cookie{user.Cookie}, map[string]string{"Origin": "http://" + f.cfg.UserHost, "Content-Type": "application/json", "X-Elevated-Token": token})
		if r.Code != http.StatusOK && r.Code != http.StatusNoContent {
			t.Fatal(r.Code, r.Body.String())
		}
		return r.Body.Bytes()
	}
	var document lifecycle.ExportDocument
	if err = json.Unmarshal(account("/api/account/export", ""), &document); err != nil {
		t.Fatal(err)
	}
	var exported attendance.UserExport
	if document.SchemaVersion != 35 || json.Unmarshal(document.Attendance, &exported) != nil || len(exported.SignIns) != 1 || len(exported.Periods) != 1 || !exported.Periods[0].Tiers[0].Awarded {
		t.Fatal("attendance export missing", string(document.Attendance))
	}
	account("/api/account/delete", `{"confirm":"DELETE"}`)
	for _, table := range []string{"attendance_progress", "attendance_sign_ins", "attendance_awards"} {
		var count int
		if err = f.store.DB().QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=?", userID).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
	if code, _ := request(body, "http://"+f.cfg.UserHost, "attendance-sign-in-key-0001"); code != http.StatusUnauthorized {
		t.Fatal("deleted session could replay", code)
	}
}
