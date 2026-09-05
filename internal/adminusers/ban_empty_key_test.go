package adminusers

import (
	"math"
	"net/http"
	"testing"
)

func TestBanAccountWithoutAnActiveCallerKey(t *testing.T) {
	for _, generation := range []int64{0, math.MaxInt64} {
		fixture := newAdminUsersFixture(t)
		userID := fixture.seedUser("inactive-key", false)
		fixture.addSession(userID, "inactive-key-session")
		if _, err := fixture.store.DB().Exec(`DELETE FROM caller_keys WHERE user_id=?`, userID); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.DB().Exec(`INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,?,?)`, userID, generation, adminUsersTestNow); err != nil {
			t.Fatal(err)
		}
		response := fixture.request(http.MethodPost, routeBan, "https://admin.example/admin/api/users/1/ban", `{"expected_revision":"1","reason":"Account restriction","duration_seconds":60}`, userID, "NOKEYNOKEYNOKEYNOKEY12")
		if response.Code != http.StatusNoContent || fixture.revision(userID) != "2" {
			t.Fatalf("ban=%d %s", response.Code, response.Body.String())
		}
		var current int64
		if err := fixture.store.DB().QueryRow(`SELECT generation FROM caller_keys WHERE user_id=?`, userID).Scan(&current); err != nil || current != generation {
			t.Fatalf("generation=%d want=%d err=%v", current, generation, err)
		}
	}
}
