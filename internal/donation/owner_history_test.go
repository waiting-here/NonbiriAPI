package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedTerminalHistory(t *testing.T, e *donationTestEnv, user int64, count int) {
	t.Helper()
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < count; i++ {
		r, err := tx.Exec(`INSERT INTO donations(user_id,status,revision,description,created_at,updated_at,terminal_at) VALUES(?,'rejected',1,'old match',?,?,?)`, user, e.clock.Load(), e.clock.Load(), e.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		id, _ := r.LastInsertId()
		if _, err = tx.Exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'legacy',1,?,?)`, id, e.clock.Load(), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestOwnerHistoryThousandRowsResumeReplayAndRestore(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "history-owner", nil, false)
	other := e.seedUser(t, "history-other", nil, false)
	seedTerminalHistory(t, e, owner, 1005)
	seedTerminalHistory(t, e, other, 1)
	active, _ := createBrowseDonation(t, e, owner, 'a', "")
	seq := 0
	batch := func(input OwnerHistorySelection) OwnerHistoryBatch {
		t.Helper()
		seq++
		m := donationMutation(t, 'B', http.MethodPost, routeOwnerHistoryBatch, nil, input)
		m.IdempotencyKey = fmt.Sprintf("history-batch-%020d", seq)
		out, err := e.service.OwnerHistoryBatch(ctx, owner, input, m)
		if err != nil {
			t.Fatal(err)
		}
		again, err := e.service.OwnerHistoryBatch(ctx, owner, input, m)
		if err != nil || !again.Replayed || string(out.Body) != string(again.Body) {
			t.Fatal(again, err)
		}
		return out.Value
	}
	start := time.Now()
	out := batch(OwnerHistorySelection{Action: "hide"})
	if out.ChangedCount != "100" || out.NextCursor == nil {
		t.Fatal(out)
	}
	firstCursor := *out.NextCursor
	// The cursor is bound to owner, action, and the exact filter.
	wrong := OwnerHistorySelection{Action: "hide", Query: "changed", Cursor: firstCursor}
	if _, err := e.service.OwnerHistoryBatch(ctx, owner, wrong, donationMutation(t, 'X', http.MethodPost, routeOwnerHistoryBatch, nil, wrong)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	total := 100
	for out.NextCursor != nil {
		out = batch(OwnerHistorySelection{Action: "hide", Cursor: *out.NextCursor})
		n, _ := strconvParseNonnegative(out.ChangedCount)
		total += int(n)
	}
	if total != 1005 {
		t.Fatal(total)
	}
	t.Logf("hide 1005 terminal rows with replay: %s", time.Since(start))
	hidden, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{OwnerView: "hidden"}, pagination.Default())
	if err != nil || hidden.Pagination.TotalItems != "1005" {
		t.Fatal(hidden.Pagination, err)
	}
	current, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{OwnerView: "current"}, pagination.Default())
	if err != nil || current.Pagination.TotalItems != "1" || current.Data[0].ID != active.ID {
		t.Fatal(current, err)
	}
	restore := batch(OwnerHistorySelection{Action: "restore"})
	// A single restore/re-hide after the range is frozen receives a new preference ID.
	id := int64(1005)
	set := func(hidden bool, seed byte) {
		t.Helper()
		_, err := e.service.SetOwnerHistory(ctx, owner, id, hidden, donationMutation(t, seed, http.MethodPut, routeOwnerHistory, []int64{id}, map[string]any{"hidden": hidden}))
		if err != nil {
			t.Fatal(err)
		}
	}
	set(false, 'c')
	set(true, 'd')
	for restore.NextCursor != nil {
		restore = batch(OwnerHistorySelection{Action: "restore", Cursor: *restore.NextCursor})
	}
	hidden, err = e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{OwnerView: "hidden"}, pagination.Default())
	if err != nil || hidden.Pagination.TotalItems != "1" {
		t.Fatal(hidden.Pagination, err)
	}
	if _, err = e.service.SetOwnerHistory(ctx, other, id, false, donationMutation(t, 'e', http.MethodPut, routeOwnerHistory, []int64{id}, map[string]any{"hidden": false})); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	activeID := parseTestID(t, active.ID)
	if _, err = e.service.SetOwnerHistory(ctx, owner, activeID, true, donationMutation(t, 'f', http.MethodPut, routeOwnerHistory, []int64{activeID}, map[string]any{"hidden": true})); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	var revision, updated, terminal int64
	if err = e.store.DB().QueryRow("SELECT revision,updated_at,terminal_at FROM donations WHERE id=?", id).Scan(&revision, &updated, &terminal); err != nil || revision != 1 || updated != donationTestNow || terminal != donationTestNow {
		t.Fatal(revision, updated, terminal, err)
	}
	e.auth.denyOwner.Store(true)
	if _, err = e.service.SetOwnerHistory(ctx, owner, id, true, donationMutation(t, 'd', http.MethodPut, routeOwnerHistory, []int64{id}, map[string]any{"hidden": true})); !errors.Is(err, ErrForbidden) {
		t.Fatal("replay authority", err)
	}
}
func strconvParseNonnegative(v string) (int64, error) {
	if v == "0" {
		return 0, nil
	}
	return strconvParse(v)
}

func TestOwnerHistorySameSecondTransitionsAndLogicalExpiry(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "transition-owner", nil, false)
	e.seedUser(t, "", nil, true)
	seedTerminalHistory(t, e, owner, 100)
	roots := make([]Donation, 7)
	physical := make([]int64, 7)
	for i := range roots {
		roots[i], physical[i] = createBrowseDonation(t, e, owner, byte('a'+i), "")
	}
	// Approve the root used for a real termination before freezing the batch.
	id := parseTestID(t, roots[3].ID)
	keyID := parseTestID(t, roots[3].Keys[0].ID)
	approve := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "test", KeySettings: []KeySetting{{DonationKeyID: keyID, Enabled: true}}}
	if _, err := e.service.ReviewAdmin(ctx, donationMutation(t, 'p', http.MethodPost, routeAdminReview, []int64{id}, approve), id, approve); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{4, 5, 6} {
		expires := donationTestNow
		if i == 5 {
			expires++
		}
		if _, err := e.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE donation_id=?", expires, roots[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.DB().Exec("UPDATE donations SET description='not selected' WHERE id=?", roots[6].ID); err != nil {
		t.Fatal(err)
	}
	selection := OwnerHistorySelection{Action: "hide", Query: "match"}
	// Give real roots the initial selected description except the edited root.
	if _, err := e.store.DB().Exec("UPDATE donations SET description='old match' WHERE id BETWEEN ? AND ?", roots[0].ID, roots[5].ID); err != nil {
		t.Fatal(err)
	}
	first, err := e.service.OwnerHistoryBatch(ctx, owner, selection, donationMutation(t, 'q', http.MethodPost, routeOwnerHistoryBatch, nil, selection))
	if err != nil || first.Value.NextCursor == nil {
		t.Fatal(first, err)
	}
	id = parseTestID(t, roots[0].ID)
	if _, err = e.service.Withdraw(ctx, owner, id, donationMutation(t, 'r', http.MethodPost, routeWithdraw, []int64{id}, map[string]any{"expected_revision": "1"}), RevisionInput{ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	id = parseTestID(t, roots[1].ID)
	reject := ReviewInput{Decision: "reject", ExpectedRevision: 1, Reason: "test"}
	if _, err = e.service.ReviewAdmin(ctx, donationMutation(t, 's', http.MethodPost, routeAdminReview, []int64{id}, reject), id, reject); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = e.service.PrepareEndpointKeyDeletion(ctx, tx, owner, []int64{physical[2]}, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	id = parseTestID(t, roots[3].ID)
	terminate := TerminateInput{ExpectedRevision: 2, Confirmation: "terminate"}
	if _, err = e.service.Terminate(ctx, owner, id, donationMutation(t, 't', http.MethodPost, routeTerminate, []int64{id}, terminate), terminate); err != nil {
		t.Fatal(err)
	}
	id = parseTestID(t, roots[6].ID)
	edit := EditInput{ExpectedRevision: 1, Description: "new match"}
	em := donationMutation(t, 'u', http.MethodPatch, routeDonation, []int64{id}, edit)
	if _, err = e.service.Edit(ctx, owner, id, em, edit); err != nil {
		t.Fatal(err)
	}
	if replay, err := e.service.Edit(ctx, owner, id, em, edit); err != nil || !replay.Replayed {
		t.Fatal(replay, err)
	}
	var edits int
	if err = e.store.DB().QueryRow("SELECT COUNT(*) FROM donation_reviews WHERE donation_id=? AND action='note_update' AND note=''", id).Scan(&edits); err != nil || edits != 1 {
		t.Fatal(edits, err)
	}
	e.clock.Add(1)
	tx, err = e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{4, 5} {
		if _, err = e.service.MaterializeExpiryTx(ctx, tx, parseTestID(t, roots[i].ID), e.clock.Load()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	selection.Cursor = *first.Value.NextCursor
	next, err := e.service.OwnerHistoryBatch(ctx, owner, selection, donationMutation(t, 'v', http.MethodPost, routeOwnerHistoryBatch, nil, selection))
	if err != nil || next.Value.ChangedCount != "1" || next.Value.NextCursor != nil {
		t.Fatal(next, err)
	}
	for i, d := range roots {
		var at any
		err = e.store.DB().QueryRow("SELECT owner_hidden_at FROM donation_owner_preferences WHERE donation_id=?", d.ID).Scan(&at)
		if i == 4 {
			if err != nil || at == nil {
				t.Fatal(i, at, err)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("later transition entered old batch", i, at, err)
		}
	}
}

func TestOwnerHistoryViewsExportDeletionAndExpiryCursor(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "view-owner", nil, false)
	d, _ := createBrowseDonation(t, e, owner, 'a', "")
	id := parseTestID(t, d.ID)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE donation_id=?", e.clock.Load(), id); err != nil {
		t.Fatal(err)
	}
	page, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{OwnerView: "history", Status: "expired"}, pagination.Default())
	if err != nil || page.Pagination.TotalItems != "1" {
		t.Fatal(page, err)
	}
	keys, err := e.service.KeysOwnerPage(ctx, owner, id, pagination.Default(), "ended")
	if err != nil || keys.Pagination.TotalItems != "1" {
		t.Fatal(keys, err)
	}
	before, err := e.service.KeysOwnerPage(ctx, owner, id, pagination.Default(), "current")
	if err != nil || before.Pagination.TotalItems != "0" {
		t.Fatal(before, err)
	}
	m := donationMutation(t, 'h', http.MethodPut, routeOwnerHistory, []int64{id}, map[string]any{"hidden": true})
	if _, err = e.service.SetOwnerHistory(ctx, owner, id, true, m); err != nil {
		t.Fatal(err)
	}
	var status string
	var terminal any
	if err = e.store.DB().QueryRow("SELECT status,terminal_at FROM donations WHERE id=?", id).Scan(&status, &terminal); err != nil || status != "pending" || terminal != nil {
		t.Fatal(status, terminal, err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	values, err := e.service.ExportUserTx(ctx, tx, owner, e.clock.Load(), 10000)
	if err != nil || len(values) != 1 || values[0].OwnerHiddenAt == nil {
		t.Fatal(values, err)
	}
	if err = e.service.PrepareAccountDeletion(ctx, tx, owner, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM donation_owner_preferences WHERE user_id=?", owner).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	seedTerminalHistory(t, e, owner, 101)
	input := OwnerHistorySelection{Action: "hide"}
	first, err := e.service.OwnerHistoryBatch(ctx, owner, input, donationMutation(t, 'i', http.MethodPost, routeOwnerHistoryBatch, nil, input))
	if err != nil || first.Value.NextCursor == nil {
		t.Fatal(first, err)
	}
	e.clock.Add(ownerHistoryLifetime + 1)
	input.Cursor = *first.Value.NextCursor
	if _, err = e.service.OwnerHistoryBatch(ctx, owner, input, donationMutation(t, 'j', http.MethodPost, routeOwnerHistoryBatch, nil, input)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestOwnerHistoryKeyViewsKeepActiveRootAndLegacyAll(t *testing.T) {
	e := newDonationTestEnv(t)
	ctx := context.Background()
	owner := e.seedUser(t, "partial-owner", nil, false)
	_, a := e.seedEndpointKey(t, owner, 'a')
	_, b := e.seedEndpointKey(t, owner, 'b')
	d := e.createDonation(t, owner, a, b)
	id := parseTestID(t, d.ID)
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = e.service.PrepareEndpointKeyDeletion(ctx, tx, owner, []int64{a}, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"current", "ended", "all", ""} {
		page, err := e.service.KeysOwnerPage(ctx, owner, id, pagination.Default(), view)
		want := "1"
		if view == "all" || view == "" {
			want = "2"
		}
		if err != nil || page.Pagination.TotalItems != want || len(page.Data) != int(want[0]-'0') {
			t.Fatal(view, page, err)
		}
	}
	page, err := e.service.DonationsOwnerPage(ctx, owner, ManagementFilter{OwnerView: "current"}, pagination.Default())
	if err != nil || page.Pagination.TotalItems != "1" {
		t.Fatal(page, err)
	}
	legacy, _, err := e.service.ListOwner(ctx, owner, 0, 20)
	if err != nil || len(legacy) != 1 {
		t.Fatal(legacy, err)
	}
}
