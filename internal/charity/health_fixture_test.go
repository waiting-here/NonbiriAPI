package charity

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
)

func (e *charityTestEnv) serviceHealthID(t *testing.T, handle claim.Handle) int64 {
	t.Helper()
	var id int64
	if err := e.store.DB().QueryRow(`SELECT health_id_snapshot FROM dispatch_service_health WHERE claim_id=?`, handle.ClaimID()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *charityTestEnv) seedServiceStreak(t *testing.T, handle claim.Handle, streak int64) {
	t.Helper()
	if _, err := e.store.DB().Exec(`UPDATE donation_service_health SET failure_streak=? WHERE id=?`, u128Blob(t, streak), e.serviceHealthID(t, handle)); err != nil {
		t.Fatal(err)
	}
}
