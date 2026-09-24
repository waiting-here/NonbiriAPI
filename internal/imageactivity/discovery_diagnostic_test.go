package imageactivity

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDiscoveryStatusIsVisibleOnlyToAdministratorsAndWithinRetention(t *testing.T) {
	for _, status := range []int32{401, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newFixture(t)
			f.configure(t)
			f.upstream.catalogStatus.Store(status)
			result, err := f.service.RefreshModels(f.ctx(f.admin), f.admin, f.key())
			if err != nil {
				t.Fatal(err)
			}
			id := result.Value.Operation.ID
			f.wait(t, func() bool {
				current, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, id)
				return err == nil && current.State == "failed"
			})
			// The diagnostic repository uses wall time; align its fixture timestamps
			// with the activity clock before exercising retention projection.
			if _, err := f.database.Exec("UPDATE request_error_bodies SET created_at=?,expires_at=? WHERE operation_id=?", testNow, testNow+30*86400, id); err != nil {
				t.Fatal(err)
			}
			current, err := f.service.GetRefresh(f.ctx(f.admin), f.admin, id)
			if err != nil || current.HTTPStatus == nil || *current.HTTPStatus != int64(status) || current.ErrorCode == nil || *current.ErrorCode != "upstream_failed" {
				t.Fatalf("status projection: %+v %v", current, err)
			}
			data, _ := json.Marshal(current)
			if strings.Contains(string(data), "private provider") || strings.Contains(string(data), "secret") {
				t.Fatal("provider detail leaked")
			}
			for _, user := range []int64{f.user, f.steward, f.trainee} {
				if _, err := f.service.GetRefresh(f.ctx(user), user, id); err == nil {
					t.Fatal("non-admin discovery access")
				}
			}
			if _, err := f.database.Exec("UPDATE request_error_bodies SET expires_at=created_at WHERE operation_id=?", id); err != nil {
				t.Fatal(err)
			}
			current, err = f.service.GetRefresh(f.ctx(f.admin), f.admin, id)
			if err != nil || current.HTTPStatus != nil {
				t.Fatalf("expired status: %+v %v", current, err)
			}
		})
	}
}
