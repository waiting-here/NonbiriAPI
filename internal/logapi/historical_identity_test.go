package logapi

import (
	"context"
	"testing"
	"time"
)

func TestHistoricalOriginSurvivesDeletionButNewAccountCannotReadIt(t *testing.T) {
	f := newLifecycleLogDatabase(t)
	ctx := context.Background()
	now := int64(1700000100)
	f.repo.now = func() time.Time { return time.Unix(now, 0) }
	old := seedLifecycleLogUser(t, f.store.DB(), "123456789012345678", false)
	requestID := logOpaqueID("req_", 42)
	root := insertLifecycleRequestLog(t, f.store.DB(), requestID, old, string(ResultSuccess), int64(200), nil, now-10, now-5, 5, 0)
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.repo.PrepareLifecycleAccountDeletion(ctx, tx, old, now); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE logical_requests SET user_id=NULL WHERE user_id=?`, old); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec(`DELETE FROM users WHERE id=?`, old); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	fresh := seedLifecycleLogUser(t, f.store.DB(), "123456789012345678", false)
	page, err := f.repo.ListUser(ctx, fresh, ListFilter{})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("new account gained historical ownership", page, err)
	}
	admin, err := f.repo.ListAdmin(ctx, ListFilter{UserID: &old})
	if err != nil || len(admin.Data) != 1 {
		t.Fatal(admin, err)
	}
	identity := admin.Data[0].OriginIdentity
	if identity.UserID == nil || identity.DiscordID == nil || *identity.DiscordID != "123456789012345678" || !identity.Deleted || identity.Unknown || admin.Data[0].UserID != nil {
		t.Fatal(admin.Data[0])
	}
	if _, err = f.store.DB().Exec(`UPDATE request_logs SET origin_discord_id='987654321098765432' WHERE id=?`, root); err == nil {
		t.Fatal("immutable request identity rewritten")
	}
	deadline := now - 5 + requestLogRetentionSeconds
	retained, err := f.repo.RetainLifecycleRequestLogs(ctx, deadline-1, 10, time.Now().Add(time.Second))
	if err != nil || retained.RequestLogsDeleted != 0 {
		t.Fatal(retained, err)
	}
	retained, err = f.repo.RetainLifecycleRequestLogs(ctx, deadline, 10, time.Now().Add(time.Second))
	if err != nil || retained.RequestLogsDeleted != 1 {
		t.Fatal(retained, err)
	}
}
