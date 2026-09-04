package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestExportUserEntriesIsOwnerScopedWideExactBoundedAndTransactionLocal(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	seedTx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, seedTx, "export-owner")
	otherUserID, otherWallet := seedLedgerUser(t, seedTx, "export-other")
	external, err := CodedAccount(ctx, seedTx, "external")
	if err != nil {
		t.Fatal(err)
	}

	wide := mustAmount(t, "9000000000000000")
	firstID := mustLedgerID(t, "op_")
	first, err := NewAdminUserAdjustment(
		Meta{OperationID: firstID, ActorUserID: userID, CreatedAt: ledgerTestNow},
		wallet.ID, external.ID, wide, 0, Amount{}, "wide owner export",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, seedTx, first); err != nil {
		t.Fatal(err)
	}
	secondID := mustLedgerID(t, "op_")
	second, err := NewAntiAbusePenalty(
		Meta{OperationID: secondID, ActorUserID: userID, CreatedAt: ledgerTestNow + 1},
		wallet.ID, external.ID, AmountFromMilli(7), "owner penalty",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, seedTx, second); err != nil {
		t.Fatal(err)
	}
	otherID := mustLedgerID(t, "op_")
	other, err := NewAdminUserAdjustment(
		Meta{OperationID: otherID, ActorUserID: otherUserID, CreatedAt: ledgerTestNow + 2},
		otherWallet.ID, external.ID, AmountFromMilli(9_000), 0, Amount{}, "foreign owner export",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, seedTx, other); err != nil {
		t.Fatal(err)
	}
	if err := seedTx.Commit(); err != nil {
		t.Fatal(err)
	}

	readTx := beginLedgerTestTx(t, store.DB())
	entries, err := ExportUserEntries(ctx, readTx, userID, 2)
	if err != nil {
		t.Fatalf("export owner ledger: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("owner entries = %+v", entries)
	}
	if entries[0].OperationID != firstID || entries[0].Kind != KindAdminUserAdjustment ||
		entries[0].SourceType != "operation" || entries[0].SourceID != firstID ||
		entries[0].Delta != "9000000000000" || entries[0].CreatedAt != ledgerTestNow {
		t.Fatalf("wide export entry = %+v", entries[0])
	}
	if entries[1].OperationID != secondID || entries[1].Kind != KindAntiAbusePenalty ||
		entries[1].Delta != "-0.007" || entries[1].CreatedAt != ledgerTestNow+1 {
		t.Fatalf("negative export entry = %+v", entries[1])
	}
	for _, entry := range entries {
		if entry.OperationID == otherID {
			t.Fatalf("foreign operation leaked into owner export: %+v", entry)
		}
	}
	if _, err := ExportUserEntries(ctx, readTx, userID, 1); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("bounded export error = %v, want too large", err)
	}
	if _, err := ExportUserEntries(ctx, readTx, userID, 0); !errors.Is(err, ErrInvalidExport) {
		t.Fatalf("zero limit error = %v", err)
	}
	if _, err := ExportUserEntries(ctx, readTx, userID, MaxUserExportEntries+1); !errors.Is(err, ErrInvalidExport) {
		t.Fatalf("oversized limit error = %v", err)
	}
	if err := readTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	mutationTx := beginLedgerTestTx(t, store.DB())
	current, err := UserAccount(ctx, mutationTx, userID)
	if err != nil {
		t.Fatal(err)
	}
	thirdID := mustLedgerID(t, "op_")
	third, err := NewAdminUserAdjustment(
		Meta{OperationID: thirdID, ActorUserID: userID, CreatedAt: ledgerTestNow + 3},
		current.ID, external.ID, AmountFromMilli(1), 0, Amount{}, "transaction-local export",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, mutationTx, third); err != nil {
		t.Fatal(err)
	}
	inside, err := ExportUserEntries(ctx, mutationTx, userID, 3)
	if err != nil || len(inside) != 3 || inside[2].OperationID != thirdID || inside[2].Delta != "0.001" {
		t.Fatalf("transaction-local export = (%+v,%v)", inside, err)
	}
	if err := mutationTx.Rollback(); err != nil {
		t.Fatal(err)
	}

	afterTx := beginLedgerTestTx(t, store.DB())
	defer afterTx.Rollback()
	after, err := ExportUserEntries(ctx, afterTx, userID, 2)
	if err != nil || len(after) != 2 {
		t.Fatalf("export after rollback = (%+v,%v)", after, err)
	}
}

func TestFormatDisplayCreditsPreservesFullSM128Precision(t *testing.T) {
	for _, test := range []struct {
		milli string
		want  string
	}{
		{milli: "0", want: "0"},
		{milli: "1234567890123456789012345", want: "1234567890123456789012.345"},
		{milli: "-1234567890123456789012345", want: "-1234567890123456789012.345"},
		{milli: "100", want: "0.1"},
		{milli: "-1", want: "-0.001"},
	} {
		amount := mustAmount(t, test.milli)
		if got := formatDisplayCredits(amount.Big()); got != test.want {
			t.Fatalf("format %s = %q, want %q", test.milli, got, test.want)
		}
	}
}
