package antiabuse

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestPenaltyReadRetentionEvidenceAndParentOwnership(t *testing.T) {
	f := newAbuseFixture(t)
	user, other := f.user(), f.user()
	start := f.clock.Load()
	f.set(KeyCharityViolationDeductMilli, "1000")
	f.set(KeyCharityViolationBanSeconds, "10")
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]p/m", 1); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	page, err := ReadCasesTx(ctx, tx, user, start, pagination.Default(), "", "active")
	if err != nil || len(page.Data) != 1 || page.Data[0].Kind != "ban" {
		t.Fatal("active case", page, err)
	}
	id := page.Data[0].ID
	detail, err := ReadCaseTx(ctx, tx, user, start, id, pagination.Default())
	if err != nil || len(detail.Actions.Data) != 1 {
		t.Fatal("case actions", detail, err)
	}
	action, _ := strconv.ParseInt(detail.Actions.Data[0].ID, 10, 64)
	evidence, err := ReadEvidenceTx(ctx, tx, user, start, id, action, pagination.Default())
	if err != nil || len(evidence.Members.Data) != 1 || !evidence.Members.Data[0].RequestLogAvailable {
		t.Fatal("evidence", evidence, err)
	}
	if _, err := ReadCaseTx(ctx, tx, other, start, id, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-owner case", err)
	}
	if _, err := ReadEvidenceTx(ctx, tx, other, start, id, action, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-owner action", err)
	}
	if _, err := ReadEvidenceTx(ctx, tx, user, start, id, action+100, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("unrelated action", err)
	}
	now := start + 31*24*60*60
	evidence, err = ReadEvidenceTx(ctx, tx, user, now, id, action, pagination.Default())
	if err != nil || len(evidence.Members.Data) != 1 || evidence.Members.Data[0].RequestLogAvailable {
		t.Fatal("evidence followed log expiry", evidence, err)
	}
	page, err = ReadCasesTx(ctx, tx, user, now, pagination.Default(), "ban", "ended")
	if err != nil || len(page.Data) != 1 || page.Data[0].EndedAt == nil || *page.Data[0].EndedAt != start+10 {
		t.Fatal("logical expiry depends on worker", page, err)
	}
	if _, err := ReadCaseTx(ctx, tx, user, start+10+RetentionSeconds, id, pagination.Default()); !errors.Is(err, ErrNotFound) {
		t.Fatal("exact retention boundary", err)
	}
}

func TestManualTakeoverEndsOnlyAutomaticCaseAndPreservesSnapshot(t *testing.T) {
	f := newAbuseFixture(t)
	user := f.user()
	actor := f.user()
	start := f.clock.Load()
	f.set(KeyCharityViolationBanSeconds, "90")
	if _, err := f.service.RecordShort(context.Background(), user, "[公益]p/m", 2); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := EndAutomaticTx(ctx, tx, user, "ban", actor, start+10, true, nil); err != nil {
		t.Fatal(err)
	}
	page, err := ReadCasesTx(ctx, tx, user, start+11, pagination.Default(), "ban", "ended")
	if err != nil || len(page.Data) != 1 || page.Data[0].Result != "adjusted" || *page.Data[0].EndedAt != start+10 {
		t.Fatal(page, err)
	}
	detail, err := ReadCaseTx(ctx, tx, user, start+11, page.Data[0].ID, pagination.Default())
	if err != nil || len(detail.Actions.Data) != 2 || detail.Actions.Data[0].Action != "adjust" || detail.Actions.Data[0].EndsAt != nil || *detail.Actions.Data[0].PreviousEndsAt != start+90 {
		t.Fatal(detail, err)
	}
	if err := PruneTx(ctx, tx, start+10+RetentionSeconds, 100); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM abuse_evidence`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphaned evidence", count, err)
	}
}
