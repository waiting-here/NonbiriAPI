package observability

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestAccessNumberedSnapshotFiltersAndDeletion(t *testing.T) {
	repository, userID := newObservedDatabase(t)
	ctx := context.Background()
	encoded, err := json.Marshal(Source{EffectiveIP: "192.0.2.72", IPQuality: "direct_peer", UserAgent: "Example/1"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range 125 {
		id, err := db.GenerateOpaqueID("aev_")
		if err != nil {
			t.Fatal(err)
		}
		status := 200
		if index == 124 {
			status = 429
		}
		if _, err = repository.db.Exec(`INSERT INTO audit_access_events(id,user_id,path_kind,method,http_status,response_kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'models','GET',?,'api_json','192.0.2.72','direct_peer',?,?)`, id, userID, status, string(encoded), int64(1_800_000_000-100+index/2)); err != nil {
			t.Fatal(err)
		}
	}
	read := func(filter AccessFilter) NumberedAccessPage {
		t.Helper()
		tx, err := repository.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		page, err := ListAccessNumberedTx(ctx, tx, filter)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return page
	}
	filter := AccessFilter{From: 1_799_999_000, To: 1_800_000_000, UserID: userID, Limit: 20, Page: 1}
	first := read(filter)
	if first.TotalItems != "125" || first.TotalPages != "7" || len(first.Data) != 20 {
		t.Fatal(first)
	}
	filter.Page = 7
	filter.WatermarkSet = true
	filter.Watermark, _ = strconv.ParseInt(first.Watermark, 10, 64)
	filter.ExpectedSet, filter.ExpectedTotal = true, 125
	last := read(filter)
	if len(last.Data) != 5 || last.Changed {
		t.Fatal(last)
	}
	newID, _ := db.GenerateOpaqueID("aev_")
	if _, err = repository.db.Exec(`INSERT INTO audit_access_events(id,user_id,path_kind,method,http_status,response_kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'models','GET',200,'api_json','192.0.2.72','direct_peer',?,?)`, newID, userID, string(encoded), int64(1_800_000_000-1)); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.db.Exec(`UPDATE observability_state SET access_rows=(SELECT count(*) FROM audit_access_events) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if after := read(filter); after.TotalItems != "125" || after.Changed {
		t.Fatal("late row entered frozen page", after)
	}
	if _, err = repository.db.Exec(`DELETE FROM audit_access_events WHERE id=?`, last.Data[0].ID); err != nil {
		t.Fatal(err)
	}
	if after := read(filter); after.TotalItems != "124" || !after.Changed || len(after.Data) != 4 {
		t.Fatal("deletion not reflected", after)
	}
	filter.Page, filter.StatusClass = 1, 4
	filter.WatermarkSet, filter.ExpectedSet = false, false
	filtered := read(filter)
	if filtered.TotalItems != "1" || len(filtered.Data) != 1 || filtered.Data[0].HTTPStatus != 429 {
		t.Fatal("filter applied after page", filtered)
	}
}
