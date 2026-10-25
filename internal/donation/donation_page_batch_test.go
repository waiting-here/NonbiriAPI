package donation

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestDonationPageBatchMatchesRetainedSummaryProjection(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "batch-summary-owner", nil, false)
	var keys []int64
	for i, source := range []string{"a", "b", "a", "c", "d"} {
		_, key := e.seedEndpointKeyAt(t, owner, byte('a'+i), "https://"+source+".example.test/v1")
		keys = append(keys, key)
	}
	e.createDonationWithSeed(t, owner, 'q', keys...)
	expired, _ := createBrowseDonation(t, e, owner, 'r', "")
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET expires_at=? WHERE donation_id=?", e.clock.Load(), expired.ID); err != nil {
		t.Fatal(err)
	}
	addUnrelatedPageHistory(t, e, 3)
	result, err := e.store.DB().Exec(`INSERT INTO donations(user_id,status,revision,description,created_at,updated_at) VALUES(?,'pending',1,'empty pending',?,?)`, owner, e.clock.Load(), e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	emptyID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO donation_handling(donation_id,state,revision,created_at,updated_at) VALUES(?,'pending',1,?,?)`, emptyID, e.clock.Load(), e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	tx, err := e.service.beginDonationPageTx(t.Context(), reviewerAdmin, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT id FROM donations ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := readDonationPageSummariesTx(t.Context(), tx, ids, e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := readUseSummariesTx(t.Context(), tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		header, err := logicalHeaderTx(t.Context(), tx, id, e.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		header.UseSummary = summaries[id]
		summary, err := donationSummaryTx(t.Context(), tx, header, e.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		want := AdminDonationSummary{DonationSummary: summary, Owner: header.Owner, Reviewer: header.Reviewer, Handling: header.Handling}
		item := batch[strconv.FormatInt(id, 10)]
		item.summary.UseSummary = summaries[id]
		got := AdminDonationSummary{DonationSummary: item.summary, Owner: item.header.Owner, Reviewer: item.header.Reviewer, Handling: item.header.Handling}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("id=%d batch=%+v retained=%+v", id, got, want)
		}
	}
	if len(batch) != len(ids) {
		t.Fatal("page lost records")
	}
	empty := batch[strconv.FormatInt(emptyID, 10)].summary
	if empty.Status != "expired" || empty.KeyCount != "0" || empty.SourceCount != "0" || len(empty.Sources) != 0 {
		t.Fatalf("empty summary=%+v", empty)
	}
}

func TestDonationPageReaderKeepsAuthorizedSnapshotAndRechecksRevocation(t *testing.T) {
	e, actor, _ := sourceReadFixture(t)
	ctx := authz.WithStewardPermission(t.Context(), "donations.browse")
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	originalNow := e.service.now
	e.service.now = func() time.Time { close(entered); <-release; return originalNow() }
	type outcome struct {
		page Page[StewardDonationSummary]
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		defer close(finished)
		page, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{}, pagination.Default())
		done <- outcome{page, err}
	}()
	defer func() { resume(); <-finished }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("page did not establish its snapshot")
	}
	if e.store.ReadDB().Stats().InUse != 1 || e.store.DB().Stats().InUse != 0 {
		t.Fatal("pure page occupied the writer")
	}
	writeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err := e.store.DB().ExecContext(writeCtx, "DELETE FROM admin_endpoint_tags")
	cancel()
	if err != nil {
		t.Fatalf("writer did not progress: %v", err)
	}
	resume()
	got := <-done
	<-finished
	e.service.now = originalNow
	if got.err != nil || got.page.Pagination.TotalItems != "1" || len(got.page.Data) != 1 {
		t.Fatalf("snapshot changed: %+v %v", got.page, got.err)
	}
	row := got.page.Data[0]
	if row.Owner != nil || row.Reviewer != nil || row.DiscordPublicThanks != nil || row.KeyCount != "3" {
		t.Fatalf("whole-donation or redaction changed: %+v", row)
	}
	next, err := e.service.DonationsStewardPage(ctx, actor, ManagementFilter{}, pagination.Default())
	if err != nil || next.Pagination.TotalItems != "0" || len(next.Data) != 0 {
		t.Fatal("new read ignored tag removal", next, err)
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.DonationScope = authz.DonationScopeEnhanced
	for _, key := range []string{"donations.review", "donations.manage_keys", "donations.handle", "donations.discover", "donations.recurring"} {
		policy.Permissions[key] = false
	}
	if err := authz.ValidateStewardPermissions(policy); err != nil {
		t.Fatal(err)
	}
	writeDonationPolicy(t, e, actor, policy)
	if _, err = e.service.DonationsStewardPage(ctx, actor, ManagementFilter{}, pagination.Default()); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("new read ignored action revocation", err)
	}
}

func TestDonationPageTransactionsAreQueryOnlyForEveryRole(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	owner := e.seedUser(t, "page-query-owner", nil, false)
	six := int64(6)
	steward := e.seedUser(t, "page-query-steward", &six, false)
	for _, actor := range []struct {
		role reviewerRole
		id   int64
	}{{recurringOwner, owner}, {reviewerAdmin, 0}, {reviewerSteward, steward}} {
		tx, err := e.service.beginDonationPageTx(t.Context(), actor.role, actor.id)
		if err != nil {
			t.Fatal(err)
		}
		var queryOnly int
		err = tx.QueryRow("PRAGMA query_only").Scan(&queryOnly)
		if err != nil || queryOnly != 1 {
			tx.Rollback()
			t.Fatalf("role=%s queryOnly=%d error=%v", actor.role, queryOnly, err)
		}
		_, err = tx.Exec("DELETE FROM admin_endpoint_tags")
		tx.Rollback()
		if err == nil {
			t.Fatalf("role=%s accepted a write", actor.role)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.service.DonationsOwnerPage(cancelled, owner, ManagementFilter{}, pagination.Default()); err == nil {
		t.Fatal("cancelled page succeeded")
	}
}
