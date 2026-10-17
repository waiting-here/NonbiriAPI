package inactivity

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func checkHistoryPagination(t *testing.T, e *testEnv, call func(string, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	for _, kind := range []string{"runs", "audits"} {
		t.Run(kind, func(t *testing.T) {
			table := "inactivity_" + kind
			tx, err := e.store.DB().Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = tx.Exec(`DELETE FROM ` + table); err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 205)
			for i := range ids {
				ids[i], err = db.GenerateOpaqueID("op_")
				if err != nil {
					t.Fatal(err)
				}
				if kind == "runs" {
					_, err = tx.Exec(`INSERT INTO inactivity_runs(id,user_id,policy_revision,activity_epoch,due_slot,action,general_milli,game_milli,created_at,deidentify_at,retain_until) VALUES(?,NULL,1,0,?,'decay','0','0',?,?,?)`, ids[i], testNow, testNow, testNow+90*day, testNow+400*day)
				} else {
					_, err = tx.Exec(`INSERT INTO inactivity_audits(id,actor_user_id,action,policy_revision,details_json,created_at,deidentify_at,retain_until) VALUES(?,NULL,'preview',1,'{}',?,?,?)`, ids[i], testNow, testNow+90*day, testNow+400*day)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			type history struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
				NextCursor *string              `json:"next_cursor"`
				Pagination *pagination.Metadata `json:"pagination"`
			}
			read := func(query string) history {
				t.Helper()
				w := call("GET", "/admin/api/inactivity-policy/"+kind+query, "", "")
				var out history
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
					t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
				}
				return out
			}
			for _, size := range []int{10, 20, 50, 100} {
				for _, page := range []int{1, 2, 3, 999} {
					out := read(fmt.Sprintf("?page=%d&page_size=%d", page, size))
					last := (len(ids) + size - 1) / size
					actual := min(page, last)
					offset := (actual - 1) * size
					want := pagination.Metadata{Page: strconv.Itoa(actual), PageSize: size, TotalItems: "205", TotalPages: strconv.Itoa(last)}
					if out.Pagination == nil || *out.Pagination != want || out.NextCursor != nil || len(out.Data) != min(size, len(ids)-offset) {
						t.Fatalf("page metadata: %+v", out)
					}
					for i, item := range out.Data {
						if item.ID != ids[len(ids)-1-offset-i] {
							t.Fatalf("unstable tied-timestamp ordering at %d", i)
						}
					}
				}
			}
			if out := read("?page=1"); out.Pagination.PageSize != 20 {
				t.Fatal("numbered default changed")
			}
			old := read("")
			if len(old.Data) != 100 || old.Pagination != nil || old.NextCursor == nil {
				t.Fatal("legacy default changed")
			}
			old = read("?page_size=7")
			if len(old.Data) != 7 || old.NextCursor == nil {
				t.Fatal("legacy size changed")
			}
			if next := read("?page_size=7&cursor=" + *old.NextCursor); len(next.Data) != 7 || next.Data[0].ID != ids[197] || next.Pagination != nil {
				t.Fatal("legacy cursor changed")
			}
			for _, bad := range []string{"?page=1&cursor=", "?page=0", "?page=2147483648", "?page=1&page_size=7", "?page=1&page=2", "?page=1&page_size=20&page_size=20"} {
				if w := call("GET", "/admin/api/inactivity-policy/"+kind+bad, "", ""); w.Code != 400 {
					t.Fatalf("accepted %s: %d", bad, w.Code)
				}
			}
			if _, err = e.store.DB().Exec(`DELETE FROM ` + table + ` WHERE rowid>(SELECT min(rowid) FROM ` + table + `)+11`); err != nil {
				t.Fatal(err)
			}
			if out := read("?page=11"); out.Pagination.Page != "1" || out.Pagination.TotalItems != "12" || len(out.Data) != 12 {
				t.Fatal("cleanup did not clamp page")
			}
			if _, err = e.store.DB().Exec(`DELETE FROM ` + table); err != nil {
				t.Fatal(err)
			}
			if out := read("?page=11"); out.Pagination.Page != "1" || out.Pagination.TotalItems != "0" || out.Data == nil || len(out.Data) != 0 {
				t.Fatal("empty page incorrect")
			}
		})
	}
}
