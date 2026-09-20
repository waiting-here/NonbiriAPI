package adminusers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestPenaltyManagementParityPagingParentsAndFinalAuthority(t *testing.T) {
	f, steward := newStewardUsersFixture(t)
	user, other := f.seedUser("penalties", false), f.seedUser("other-penalties", false)
	var id string
	var action int64
	for range 21 {
		id, _ = db.GenerateOpaqueID("abc_")
		if _, err := f.store.DB().Exec(`INSERT INTO abuse_cases(id,user_id,kind,reason_code,started_at,ends_at,ended_at,state,result) VALUES(?,?,'deduction','charity_short_content',?,?,?,'ended','applied')`, id, user, adminUsersTestNow, adminUsersTestNow, adminUsersTestNow); err != nil {
			t.Fatal(err)
		}
		r, err := f.store.DB().Exec(`INSERT INTO abuse_actions(case_id,action,occurred_at,reason_code,rules_json,statistics_json,evidence_count,evidence_bytes) VALUES(?,'trigger',?,'charity_short_content','{}','{}',0,0)`, id, adminUsersTestNow)
		if err != nil {
			t.Fatal(err)
		}
		action, _ = r.LastInsertId()
	}
	call := func(role managementRole, target int64, caseID, actionID, query string) *httptest.ResponseRecorder {
		pattern := routeUserPenalties
		if caseID != "" {
			pattern += "/{caseId}"
		}
		if actionID != "" {
			pattern += "/actions/{actionId}/evidence"
		}
		r := httptest.NewRequest(http.MethodGet, "https://example.test/penalties"+query, nil)
		r.SetPathValue("id", strconv.FormatInt(target, 10))
		r.SetPathValue("caseId", caseID)
		r.SetPathValue("actionId", actionID)
		actor := f.adminID
		if role == roleSteward {
			actor = steward
		}
		w := httptest.NewRecorder()
		f.registrar.handler(http.MethodGet, role.route(pattern))(w, r, AdminPrincipal{UserID: actor})
		return w
	}
	for _, suffix := range []struct{ c, a, q string }{{"", "", "?page=2&page_size=20&type=deduction&state=ended"}, {id, "", "?page_size=10"}, {id, strconv.FormatInt(action, 10), "?page_size=100"}} {
		a, b := call(roleAdmin, user, suffix.c, suffix.a, suffix.q), call(roleSteward, user, suffix.c, suffix.a, suffix.q)
		if a.Code != 200 || b.Code != 200 || a.Body.String() != b.Body.String() {
			t.Fatal("role mismatch", a.Code, a.Body, b.Code, b.Body)
		}
		if suffix.c == "" {
			var p struct {
				Data []json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(a.Body.Bytes(), &p); err != nil || len(p.Data) != 1 {
				t.Fatal("paging", a.Body, err)
			}
		}
	}
	for _, role := range []managementRole{roleAdmin, roleSteward} {
		for _, test := range []struct {
			target  int64
			c, a, q string
			status  int
		}{{other, id, "", "", 404}, {other, id, strconv.FormatInt(action, 10), "", 404}, {user, id, strconv.FormatInt(action-1, 10), "", 404}, {f.adminID, "", "", "", 404}, {user, "", "", "?type=unknown", 400}, {user, "", "", "?state=all", 400}, {user, "", "", "?page_size=1", 400}, {user, "", "", "?page=1&page=2", 400}, {user, id, "", "?type=ban", 400}} {
			w := call(role, test.target, test.c, test.a, test.q)
			if w.Code != test.status {
				t.Fatal(test, w.Code, w.Body)
			}
		}
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=1 WHERE id=?`, steward); err != nil {
		t.Fatal(err)
	}
	if w := call(roleSteward, user, id, strconv.FormatInt(action, 10), ""); w.Code != 403 {
		t.Fatal("stale steward", w.Code, w.Body)
	}
	f.auth.err = authz.ErrForbidden
	if w := call(roleAdmin, user, id, "", ""); w.Code != 403 {
		t.Fatal("stale admin", w.Code, w.Body)
	}
}
