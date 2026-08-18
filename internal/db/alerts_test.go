package db

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// seedAlert inserts one alert directly through the flood-safe hook so tests
// exercise the real write path. distinctRef distinguishes events that must
// not dedupe against each other.
func seedAlert(t *testing.T, st *Store, kind, ref string, subjectUserID int64) int64 {
	t.Helper()
	inserted, err := st.RecordAdminAlertBounded(context.Background(), AdminAlertInput{
		Kind:          kind,
		Message:       "message " + ref,
		Ref:           ref,
		SubjectUserID: subjectUserID,
		CreatedAt:     time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordAdminAlertBounded(%s/%s): %v", kind, ref, err)
	}
	if !inserted {
		t.Fatalf("RecordAdminAlertBounded(%s/%s): unexpectedly suppressed", kind, ref)
	}
	var id int64
	if err := st.DB().QueryRow(`SELECT id FROM admin_alerts WHERE ref = ? ORDER BY id DESC LIMIT 1`, ref).Scan(&id); err != nil {
		t.Fatalf("read back alert id: %v", err)
	}
	return id
}

func TestValidAdminAlertKind(t *testing.T) {
	for _, kind := range []string{AlertKindFetchFailed, AlertKindForwardError, AlertKindRegistrationRejected} {
		if !ValidAdminAlertKind(kind) {
			t.Errorf("registered kind %q rejected", kind)
		}
	}
	for _, kind := range []string{"", "unknown", "FETCH_FAILED", strings.Repeat("x", 200)} {
		if ValidAdminAlertKind(kind) {
			t.Errorf("unregistered kind %q accepted", kind)
		}
	}
}

func TestListAdminAlertsPagination(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-pag.db"))
	defer st.Close()

	for i := 1; i <= 7; i++ {
		seedAlert(t, st, AlertKindFetchFailed, "ref-"+string(rune('a'+i-1)), 0)
	}
	ctx := context.Background()

	// Keyset walk with limit=3 covers every alert exactly once, newest first.
	var walked []int64
	var cursor int64
	for {
		page, hasMore, err := st.ListAdminAlerts(ctx, AlertQuery{Limit: 3, BeforeID: cursor})
		if err != nil {
			t.Fatalf("ListAdminAlerts: %v", err)
		}
		if len(page) == 0 {
			t.Fatalf("page empty before end of walk (cursor=%d)", cursor)
		}
		for i := 1; i < len(page); i++ {
			if page[i].ID >= page[i-1].ID {
				t.Fatalf("page not strictly descending: %v", page)
			}
		}
		walked = append(walked, pageID(page)...)
		if len(page) < 3 || !hasMore {
			if hasMore {
				t.Fatalf("hasMore on a partial last page")
			}
			break
		}
		cursor = page[len(page)-1].ID
	}
	if len(walked) != 7 {
		t.Fatalf("walked %d alerts, want 7: %v", len(walked), walked)
	}
	seen := map[int64]bool{}
	for _, id := range walked {
		if seen[id] {
			t.Fatalf("alert %d walked twice", id)
		}
		seen[id] = true
	}

	// limit clamp: 0 and huge values both clamp to the page bound; the page
	// itself never exceeds the table size.
	page, _, err := st.ListAdminAlerts(ctx, AlertQuery{Limit: 0})
	if err != nil {
		t.Fatalf("ListAdminAlerts limit=0: %v", err)
	}
	if len(page) != 7 {
		t.Fatalf("limit=0 page = %d rows, want 7 (table size, clamped page bound)", len(page))
	}
	page, _, err = st.ListAdminAlerts(ctx, AlertQuery{Limit: 1000})
	if err != nil || len(page) != 7 {
		t.Fatalf("limit=1000 page = %d rows (err=%v), want 7", len(page), err)
	}

	// Negative cursor is rejected by validation.
	if _, _, err := st.ListAdminAlerts(ctx, AlertQuery{BeforeID: -1}); err == nil {
		t.Fatalf("negative cursor accepted")
	}
}

func pageID(page []AdminAlert) []int64 {
	out := make([]int64, 0, len(page))
	for _, a := range page {
		out = append(out, a.ID)
	}
	return out
}

func TestListAdminAlertsResolvedFilter(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-resolved.db"))
	defer st.Close()

	a1 := seedAlert(t, st, AlertKindFetchFailed, "ref-r1", 0)
	seedAlert(t, st, AlertKindForwardError, "ref-r2", 0) // id 2: stays unresolved
	a3 := seedAlert(t, st, AlertKindRegistrationRejected, "ref-r3", 0)
	ctx := context.Background()

	now := time.Now().Unix()
	if _, err := st.SetAdminAlertResolved(ctx, a3, true, now); err != nil {
		t.Fatalf("resolve a3: %v", err)
	}
	if _, err := st.SetAdminAlertResolved(ctx, a1, true, now); err != nil {
		t.Fatalf("resolve a1: %v", err)
	}

	// All states.
	all, _, err := st.ListAdminAlerts(ctx, AlertQuery{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all states: %d rows (err=%v), want 3", len(all), err)
	}
	// Unresolved only: exactly the middle alert (id 2).
	unresolved := false
	openRows, _, err := st.ListAdminAlerts(ctx, AlertQuery{Resolved: &unresolved})
	if err != nil || len(openRows) != 1 {
		t.Fatalf("unresolved: %d rows (err=%v), want 1", len(openRows), err)
	}
	if openRows[0].ID != 2 {
		t.Fatalf("unresolved row id = %d, want 2", openRows[0].ID)
	}
	// Resolved only.
	resolved := true
	resolvedRows, _, err := st.ListAdminAlerts(ctx, AlertQuery{Resolved: &resolved})
	if err != nil || len(resolvedRows) != 2 {
		t.Fatalf("resolved: %d rows (err=%v), want 2", len(resolvedRows), err)
	}
	for _, row := range resolvedRows {
		if !row.Resolved || row.ResolvedAt.IsZero() {
			t.Errorf("resolved row %d not marked resolved", row.ID)
		}
	}

	// Keyset + resolved filter: resolving an alert mid-walk stays consistent.
	var seen []int64
	var cursor int64
	for {
		page, hasMore, err := st.ListAdminAlerts(ctx, AlertQuery{Resolved: &resolved, Limit: 1, BeforeID: cursor})
		if err != nil {
			t.Fatalf("keyset resolved walk: %v", err)
		}
		if len(page) == 0 {
			break
		}
		seen = append(seen, page[0].ID)
		if !hasMore {
			break
		}
		cursor = page[0].ID
	}
	if len(seen) != 2 || seen[0] != a3 || seen[1] != a1 {
		t.Fatalf("resolved walk = %v, want [%d %d]", seen, a3, a1)
	}
}

func TestListAdminAlertsReadSideBounds(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-bounds.db"))
	defer st.Close()

	// A hostile/broken writer bypasses the write boundary entirely and puts
	// over-long, control-character, invalid-UTF-8 text straight into the
	// table; the read side must re-bound it.
	dirty := strings.Repeat("x", 9000) + "\n\t\r" + string([]byte{0xff, 0xfe}) + strings.Repeat("y", 9000)
	now := time.Now().Unix()
	if _, err := st.DB().Exec(`INSERT INTO admin_alerts (kind, message, ref, created_at) VALUES (?, ?, ?, ?)`,
		dirty, dirty, dirty, now); err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	rows, _, err := st.ListAdminAlerts(context.Background(), AlertQuery{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %d rows (err=%v), want 1", len(rows), err)
	}
	row := rows[0]
	if utf8.RuneCountInString(row.Kind) > maxAlertKindRunes || len(row.Kind) > maxAlertKindRunes*4 {
		t.Errorf("kind not bounded: %d runes", utf8.RuneCountInString(row.Kind))
	}
	if utf8.RuneCountInString(row.Message) > maxAlertMessageRunes || len(row.Message) > maxAlertMessageRunes*4 {
		t.Errorf("message not bounded: %d runes", utf8.RuneCountInString(row.Message))
	}
	if utf8.RuneCountInString(row.Ref) > maxAlertRefRunes || len(row.Ref) > maxAlertRefRunes*4 {
		t.Errorf("ref not bounded: %d runes", utf8.RuneCountInString(row.Ref))
	}
	for _, field := range []string{row.Kind, row.Message, row.Ref} {
		if !utf8.ValidString(field) {
			t.Errorf("field is not valid UTF-8: %q", field)
		}
		if strings.ContainsAny(field, "\r\n\t") {
			t.Errorf("field contains line-forgery control characters: %q", field)
		}
	}
}

func TestSetAdminAlertResolvedIdempotent(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-resolve.db"))
	defer st.Close()

	id := seedAlert(t, st, AlertKindFetchFailed, "ref-resolve", 0)
	ctx := context.Background()
	now := time.Now().Unix()

	// First resolve writes resolved_at.
	alert, err := st.SetAdminAlertResolved(ctx, id, true, now)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !alert.Resolved || alert.ResolvedAt.Unix() != now {
		t.Fatalf("resolved alert = %+v", alert)
	}

	// Repeating the same transition is a true no-op: same resolved_at.
	alert2, err := st.SetAdminAlertResolved(ctx, id, true, now+1000)
	if err != nil {
		t.Fatalf("repeat resolve: %v", err)
	}
	if !alert2.Resolved || alert2.ResolvedAt.Unix() != now {
		t.Fatalf("repeat resolve churned resolved_at: %+v", alert2)
	}

	// Reopen clears resolved_at.
	alert3, err := st.SetAdminAlertResolved(ctx, id, false, now+2000)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if alert3.Resolved || !alert3.ResolvedAt.IsZero() {
		t.Fatalf("reopened alert = %+v", alert3)
	}

	// Repeating the reopen is also a no-op.
	alert4, err := st.SetAdminAlertResolved(ctx, id, false, now+3000)
	if err != nil {
		t.Fatalf("repeat reopen: %v", err)
	}
	if alert4.Resolved || !alert4.ResolvedAt.IsZero() {
		t.Fatalf("repeat reopen changed state: %+v", alert4)
	}

	// Missing / invalid ids are ErrNotFound, never a partial write.
	for _, badID := range []int64{0, -1, 999999} {
		if _, err := st.SetAdminAlertResolved(ctx, badID, true, now); err != ErrNotFound {
			t.Errorf("id %d: err = %v, want ErrNotFound", badID, err)
		}
	}

	// The alert row is still queryable with the expected state.
	rows, _, err := st.ListAdminAlerts(ctx, AlertQuery{})
	if err != nil || len(rows) != 1 || rows[0].Resolved {
		t.Fatalf("final state: %+v (err=%v)", rows, err)
	}
}

func TestRecordAdminAlertBoundedWhitelist(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-whitelist.db"))
	defer st.Close()

	ctx := context.Background()
	for _, kind := range []string{"", "unknown", "FETCH_FAILED", strings.Repeat("z", 200)} {
		inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
			Kind: kind, Message: "m", Ref: "r", CreatedAt: time.Now(),
		})
		if err == nil {
			t.Errorf("kind %q accepted (inserted=%v)", kind, inserted)
		}
	}
	// No row leaked from the rejected kinds.
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 0 {
		t.Fatalf("rejected kinds left %d rows", n)
	}
	// Every registered kind records fine.
	for _, kind := range []string{AlertKindFetchFailed, AlertKindForwardError, AlertKindRegistrationRejected} {
		inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
			Kind: kind, Message: "m", Ref: "r-" + kind, CreatedAt: time.Now(),
		})
		if err != nil || !inserted {
			t.Errorf("kind %q: inserted=%v err=%v", kind, inserted, err)
		}
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 3 {
		t.Fatalf("registered kinds left %d rows, want 3", n)
	}
}

func TestRecordAdminAlertBoundedDedupe(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-dedupe.db"))
	defer st.Close()
	ctx := context.Background()

	user, err := st.CreateUser("discord-dedupe", "dedupe-user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Same kind+ref+subject twice: second is suppressed.
	for i := 0; i < 2; i++ {
		inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
			Kind: AlertKindFetchFailed, Message: "m", Ref: "ep-1", SubjectUserID: user.ID, CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if i == 0 && !inserted {
			t.Fatalf("first record suppressed")
		}
		if i == 1 && inserted {
			t.Fatalf("duplicate record inserted (dedupe failed)")
		}
	}

	// A different ref or a different kind is a new alert.
	if inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "ep-2", SubjectUserID: user.ID, CreatedAt: time.Now(),
	}); err != nil || !inserted {
		t.Errorf("different ref: inserted=%v err=%v", inserted, err)
	}
	if inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindForwardError, Message: "m", Ref: "ep-1", SubjectUserID: user.ID, CreatedAt: time.Now(),
	}); err != nil || !inserted {
		t.Errorf("different kind: inserted=%v err=%v", inserted, err)
	}
	// A site-wide alert (no subject) does not dedupe against the subject one.
	if inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "ep-1", CreatedAt: time.Now(),
	}); err != nil || !inserted {
		t.Errorf("site-wide vs subject-bound: inserted=%v err=%v", inserted, err)
	}

	// Resolving the pending alert frees the dedupe slot. The pending row is
	// matched by kind too: the same (kind, ref, subject) triple is the dedupe
	// key, so resolving a different-kind alert would not free this slot.
	rows, _, err := st.ListAdminAlerts(ctx, AlertQuery{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var pendingID int64
	for _, row := range rows {
		if row.Ref == "ep-1" && row.Kind == AlertKindFetchFailed && !row.Resolved && row.SubjectUserID == user.ID {
			pendingID = row.ID
			break
		}
	}
	if pendingID == 0 {
		t.Fatalf("pending ep-1 subject alert not found: %+v", rows)
	}
	if _, err := st.SetAdminAlertResolved(ctx, pendingID, true, time.Now().Unix()); err != nil {
		t.Fatalf("resolve pending: %v", err)
	}
	if inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "ep-1", SubjectUserID: user.ID, CreatedAt: time.Now(),
	}); err != nil || !inserted {
		t.Errorf("record after resolve: inserted=%v err=%v", inserted, err)
	}

	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 5 {
		t.Fatalf("rows = %d, want 5", n)
	}
}

func TestRecordAdminAlertBoundedDedupeSiteWide(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-dedupe-site.db"))
	defer st.Close()
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
			Kind: AlertKindForwardError, Message: "m", Ref: "site-ref", CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if i == 0 && !inserted {
			t.Fatalf("first record suppressed")
		}
		if i == 1 && inserted {
			t.Fatalf("duplicate site-wide record inserted")
		}
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestRecordAdminAlertBoundedFloodCap(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-flood.db"))
	defer st.Close()
	ctx := context.Background()

	// Fill the per-kind pending budget with distinct refs (each passes
	// dedupe); the cap must stop the flood.
	inserted := 0
	for i := 0; i < MaxAdminAlertsPerKind+20; i++ {
		ok, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
			Kind: AlertKindFetchFailed, Message: "m", Ref: "flood-" + intString(i), CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if ok {
			inserted++
		}
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 100 {
		t.Fatalf("rows after flood = %d, want %d (cap)", n, MaxAdminAlertsPerKind)
	}

	// A different kind has its own budget.
	ok, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindForwardError, Message: "m", Ref: "other-kind", CreatedAt: time.Now(),
	})
	if err != nil || !ok {
		t.Fatalf("other kind: ok=%v err=%v", ok, err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 101 {
		t.Fatalf("rows after other kind = %d, want 101", n)
	}

	// Resolving one pending alert of the saturated kind frees a slot.
	var fetchID int64
	if err := st.DB().QueryRow(`SELECT id FROM admin_alerts WHERE kind = ? AND resolved = 0 ORDER BY id DESC LIMIT 1`, AlertKindFetchFailed).Scan(&fetchID); err != nil {
		t.Fatalf("find pending fetch alert: %v", err)
	}
	if _, err := st.SetAdminAlertResolved(ctx, fetchID, true, time.Now().Unix()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ok, err = st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "flood-after-resolve", CreatedAt: time.Now(),
	})
	if err != nil || !ok {
		t.Fatalf("record after resolve: ok=%v err=%v", ok, err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 102 {
		t.Fatalf("rows after resolve+record = %d, want 102", n)
	}
}

func TestRecordAdminAlertBoundedDeleteRace(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-delete.db"))
	defer st.Close()
	ctx := context.Background()

	user, err := st.CreateUser("discord-race", "race-user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "pre-delete", SubjectUserID: user.ID, CreatedAt: time.Now(),
	}); err != nil || !inserted {
		t.Fatalf("pre-delete record: inserted=%v err=%v", inserted, err)
	}

	// Deleting the account removes the orphan alert row explicitly (no FK).
	if err := st.DeleteUserAccount(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUserAccount: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 0 {
		t.Fatalf("orphan alert rows = %d, want 0", n)
	}

	// A late callback against the deleted user is an atomic no-op, never an
	// orphan and never an error.
	inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "late-callback", SubjectUserID: user.ID, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("late callback: %v", err)
	}
	if inserted {
		t.Fatalf("late callback inserted an orphan alert")
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 0 {
		t.Fatalf("late callback left %d rows", n)
	}

	// Site-wide alerts are unaffected by the delete.
	if inserted, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindForwardError, Message: "m", Ref: "site-after-delete", CreatedAt: time.Now(),
	}); err != nil || !inserted {
		t.Fatalf("site-wide record: inserted=%v err=%v", inserted, err)
	}
}

func TestRecordAdminAlertBoundedConcurrentDedupe(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-conc.db"))
	defer st.Close()
	ctx := context.Background()

	const goroutines = 8
	var wg sync.WaitGroup
	inserted := make([]bool, goroutines)
	errs := make([]error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			inserted[g], errs[g] = st.RecordAdminAlertBounded(ctx, AdminAlertInput{
				Kind: AlertKindFetchFailed, Message: "m", Ref: "concurrent", CreatedAt: time.Now(),
			})
		}(g)
	}
	wg.Wait()

	insertedCount := 0
	for g := 0; g < goroutines; g++ {
		if errs[g] != nil {
			t.Fatalf("goroutine %d: %v", g, errs[g])
		}
		if inserted[g] {
			insertedCount++
		}
	}
	if insertedCount != 1 {
		t.Fatalf("inserted = %d, want exactly 1 (single-writer dedupe)", insertedCount)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM admin_alerts`); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func intString(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func TestCleanupResolvedAlerts(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-retention.db"))
	defer st.Close()
	ctx := context.Background()

	// now is captured once so the resolve timestamps and the sweep cutoff are
	// derived from the same instant, keeping the strict-< boundary deterministic.
	now := time.Now().UTC()
	const retention = 30 * 24 * time.Hour

	// a1: resolved 40 days ago -> past retention -> deleted.
	a1 := seedAlert(t, st, AlertKindFetchFailed, "old-resolved", 0)
	if _, err := st.SetAdminAlertResolved(ctx, a1, true, now.Add(-40*24*time.Hour).Unix()); err != nil {
		t.Fatalf("resolve a1: %v", err)
	}
	// a2: resolved 5 days ago -> within retention -> kept.
	a2 := seedAlert(t, st, AlertKindForwardError, "recent-resolved", 0)
	if _, err := st.SetAdminAlertResolved(ctx, a2, true, now.Add(-5*24*time.Hour).Unix()); err != nil {
		t.Fatalf("resolve a2: %v", err)
	}
	// a3: resolved exactly at the cutoff (now - retention). The predicate is
	// strict (<), so a row resolved at the cutoff survives.
	a3 := seedAlert(t, st, AlertKindRegistrationRejected, "boundary-resolved", 0)
	cutoff := now.Add(-retention)
	if _, err := st.SetAdminAlertResolved(ctx, a3, true, cutoff.Unix()); err != nil {
		t.Fatalf("resolve a3: %v", err)
	}
	// a4: pending with an ancient created_at. Retention keys off resolved_at,
	// never created_at, and never touches pending rows; this row survives
	// regardless of how old created_at is.
	if _, err := st.RecordAdminAlertBounded(ctx, AdminAlertInput{
		Kind: AlertKindFetchFailed, Message: "m", Ref: "ancient-pending",
		SubjectUserID: 0, CreatedAt: now.Add(-365 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("record ancient pending: %v", err)
	}
	var a4 int64
	if err := st.DB().QueryRow(`SELECT id FROM admin_alerts WHERE ref = ?`, "ancient-pending").Scan(&a4); err != nil {
		t.Fatalf("read back a4: %v", err)
	}
	// a5: pending, recently created -> kept (control).
	a5 := seedAlert(t, st, AlertKindForwardError, "recent-pending", 0)

	deleted, err := st.cleanupResolvedAlertsAt(ctx, now, retention)
	if err != nil {
		t.Fatalf("cleanupResolvedAlertsAt: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (only a1 past retention)", deleted)
	}

	// Survivors: a2, a3 (resolved, within/at boundary), a4, a5 (pending).
	rows, _, err := st.ListAdminAlerts(ctx, AlertQuery{})
	if err != nil {
		t.Fatalf("list after cleanup: %v", err)
	}
	got := map[int64]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	for _, want := range []int64{a2, a3, a4, a5} {
		if !got[want] {
			t.Errorf("alert %d missing after cleanup", want)
		}
	}
	if got[a1] {
		t.Errorf("a1 (past retention) still present after cleanup")
	}
	if len(rows) != 4 {
		t.Fatalf("rows after cleanup = %d, want 4: %+v", len(rows), rows)
	}

	// Idempotent: a second sweep against the same cutoff deletes nothing.
	deleted2, err := st.cleanupResolvedAlertsAt(ctx, now, retention)
	if err != nil || deleted2 != 0 {
		t.Fatalf("second sweep: deleted=%d err=%v, want 0/nil", deleted2, err)
	}

	// A zero-retention sweep removes every resolved row but no pending row,
	// proving the resolved predicate and the pending exemption together.
	deleted3, err := st.cleanupResolvedAlertsAt(ctx, now, 0)
	if err != nil {
		t.Fatalf("zero-retention sweep: %v", err)
	}
	if deleted3 != 2 { // a2, a3 resolved -> removed
		t.Fatalf("zero-retention deleted = %d, want 2", deleted3)
	}
	remaining, _, err := st.ListAdminAlerts(ctx, AlertQuery{})
	if err != nil || len(remaining) != 2 { // a4, a5 pending
		t.Fatalf("after zero-retention: %d rows (err=%v), want 2 pending", len(remaining), err)
	}
	for _, r := range remaining {
		if r.Resolved {
			t.Errorf("resolved row %d survived zero-retention sweep", r.ID)
		}
	}
}

func TestCleanupResolvedAlertsValidation(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "alerts-retention-arg.db"))
	defer st.Close()

	// A negative retention would back-date the cutoff into the future and wipe
	// all resolved rows; it is rejected as a programming error. Zero is a
	// legitimate "purge all resolved" window and is accepted elsewhere.
	if _, err := st.CleanupResolvedAlerts(context.Background(), -time.Second); err == nil {
		t.Fatalf("negative retention accepted")
	}
}
