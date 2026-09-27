package riskaudit

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestUserRequestNumberedSnapshotPagesAndDeletion(t *testing.T) {
	f := newAuditFixture(t)
	userID := f.user(1)
	for range 125 {
		f.source(userID, "self", "192.0.2.61", "direct_peer", "Example/1", "model", "success", 0)
	}
	w := Window{From: f.now - 600, To: f.now, Kind: "total", Limit: 20, Page: 1}
	first, err := f.repository.User(context.Background(), Actor{Admin: true, UserID: f.admin}, userID, w)
	if err != nil || first.Requests.TotalItems != "125" || first.Requests.TotalPages != "7" || len(first.Requests.Items) != 20 {
		t.Fatal(first.Requests, err)
	}
	w.Page, w.Limit = 7, 20
	w.WatermarkSet = true
	w.Watermark, _ = strconv.ParseInt(first.Requests.Watermark, 10, 64)
	w.ExpectedSet, w.ExpectedTotal = true, 125
	last, err := f.repository.User(context.Background(), Actor{Admin: true, UserID: f.admin}, userID, w)
	if err != nil || last.Requests.Page != "7" || len(last.Requests.Items) != 5 || last.Requests.Changed {
		t.Fatal(last.Requests, err)
	}
	newID := f.source(userID, "self", "192.0.2.61", "direct_peer", "Example/1", "model", "success", 0)
	last, err = f.repository.User(context.Background(), Actor{Admin: true, UserID: f.admin}, userID, w)
	if err != nil || last.Requests.TotalItems != "125" || last.Requests.Changed {
		t.Fatal("insert escaped watermark", last.Requests, err)
	}
	f.exec(`DELETE FROM request_source_facts WHERE request_log_id=?`, newID)
	f.exec(`UPDATE request_source_facts SET user_id=NULL WHERE request_log_id=?`, last.Requests.Items[0].LogID)
	last, err = f.repository.User(context.Background(), Actor{Admin: true, UserID: f.admin}, userID, w)
	if err != nil || last.Requests.TotalItems != "124" || !last.Requests.Changed || len(last.Requests.Items) != 4 {
		t.Fatal("deleted page not reconciled", last.Requests, err)
	}
}

func TestUserNumberedHTTPRejectsWatermarkWithoutFrozenWindow(t *testing.T) {
	f := newAuditFixture(t)
	userID := f.user(1)
	f.source(userID, "self", "192.0.2.61", "direct_peer", "Example/1", "model", "success", 0)
	for _, query := range []string{
		"watermark=1&from=1&to=2",
		"page=1&watermark=1",
		"page=1&from=1&watermark=1",
		"page=1&expected_total=1",
	} {
		req := httptest.NewRequest("GET", "/risk/users/"+strconv.FormatInt(userID, 10)+"?"+query, nil)
		req.SetPathValue("id", strconv.FormatInt(userID, 10))
		response := httptest.NewRecorder()
		serve(f.repository, "user", Actor{Admin: true, UserID: f.admin}, response, req)
		if response.Code != 400 {
			t.Fatalf("accepted unstable numbered selection %q: %d %s", query, response.Code, response.Body.String())
		}
	}
}
