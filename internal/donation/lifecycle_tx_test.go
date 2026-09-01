package donation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestExportUserTxUsesCallerSnapshotAndRejectsLimitPlusOne(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-export-tx", nil, false)
	_, keyA := environment.seedEndpointKey(t, owner, 'm')
	_, keyB := environment.seedEndpointKey(t, owner, 'n')
	first := environment.createDonationWithSeed(t, owner, 'M', keyA)
	environment.createDonationWithSeed(t, owner, 'N', keyB)
	firstID := parseTestID(t, first.ID)

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE donations SET description='caller-owned snapshot' WHERE id=?`, firstID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	exported, err := environment.service.ExportUserTx(context.Background(), tx, owner, donationTestNow, 2)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if len(exported) != 2 || exported[0].Description != "caller-owned snapshot" {
		_ = tx.Rollback()
		t.Fatalf("caller snapshot export = %+v", exported)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := environment.service.ExportUser(context.Background(), tx, owner, 2)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(wrapped) != 2 || wrapped[0].Description != "donor description" {
		t.Fatalf("wrapper observed rolled-back description: %+v", wrapped)
	}

	tx, err = environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := environment.service.ExportUserTx(context.Background(), tx, owner, donationTestNow, 1); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("limit+1 error = %v, want resource limit", err)
	}
}

func TestExportUserTxExcludesHeldOnlyDonationAndSafeNote(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-export-hold", nil, false)
	admin := environment.seedUser(t, "", nil, true)
	_, oldKey := environment.seedEndpointKey(t, owner, 'o')
	_, liveKey := environment.seedEndpointKey(t, owner, 'p')
	oldDonation := environment.createDonationWithSeed(t, owner, 'O', oldKey)
	oldID := parseTestID(t, oldDonation.ID)
	terminalAt := donationTestNow + 10
	environment.clock.Store(terminalAt)
	if _, err := environment.service.Withdraw(context.Background(), owner, oldID,
		donationMutation(t, 'P', http.MethodPost, routeWithdraw, []int64{oldID}, map[string]any{"expected_revision": 1}),
		RevisionInput{ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	liveDonation := environment.createDonationWithSeed(t, owner, 'Q', liveKey)
	liveID := parseTestID(t, liveDonation.ID)
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET safe_note='hostile-safe-note' WHERE donation_id=?`, liveID); err != nil {
		t.Fatal(err)
	}

	decisionNow := terminalAt + terminalRetention
	holdID, err := db.GenerateOpaqueID("lgh_")
	if err != nil {
		t.Fatal(err)
	}
	holdTx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holdTx.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'donation',?,'active',1,'export cutoff test',?,?,?)`, holdID, fmt.Sprintf("%d", oldID), admin,
		decisionNow-1, decisionNow+100); err != nil {
		_ = holdTx.Rollback()
		t.Fatal(err)
	}
	if _, err := holdTx.Exec(`UPDATE donations SET legal_hold_consumed=1 WHERE id=?`, oldID); err != nil {
		_ = holdTx.Rollback()
		t.Fatal(err)
	}
	if err := holdTx.Commit(); err != nil {
		t.Fatal(err)
	}

	if cleaned, err := environment.service.Cleanup(context.Background(), decisionNow, 10); err != nil || cleaned != 0 {
		t.Fatalf("held cleanup = %d, %v", cleaned, err)
	}
	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := environment.service.ExportUserTx(context.Background(), tx, owner, decisionNow, 10)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(exported) != 1 || exported[0].ID != liveDonation.ID || len(exported[0].Keys) != 1 {
		t.Fatalf("ordinary cutoff export = %+v", exported)
	}
	if exported[0].Keys[0].SafeNote != "" {
		t.Fatalf("safe note survived owner export: %+v", exported[0].Keys[0])
	}
	payload, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("safe_note")) || bytes.Contains(payload, []byte("hostile-safe-note")) {
		t.Fatalf("safe note leaked in export JSON: %s", payload)
	}
	var retained int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations WHERE id=?`, oldID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("held old donation count = %d, %v", retained, err)
	}
}

func TestCleanupTxRollbackPreservesDonationAggregate(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "donation-cleanup-rollback", nil, false)
	_, keyID := environment.seedEndpointKey(t, owner, 'r')
	value := environment.createDonationWithSeed(t, owner, 'R', keyID)
	donationID := parseTestID(t, value.ID)
	terminalAt := donationTestNow + 20
	environment.clock.Store(terminalAt)
	if _, err := environment.service.Withdraw(context.Background(), owner, donationID,
		donationMutation(t, 'S', http.MethodPost, routeWithdraw, []int64{donationID}, map[string]any{"expected_revision": 1}),
		RevisionInput{ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cleaned, err := environment.service.CleanupTx(context.Background(), tx, terminalAt+terminalRetention, 10)
	if err != nil || cleaned != 1 {
		_ = tx.Rollback()
		t.Fatalf("CleanupTx = %d, %v", cleaned, err)
	}
	var inside int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM donations WHERE id=?`, donationID).Scan(&inside); err != nil || inside != 0 {
		_ = tx.Rollback()
		t.Fatalf("inside cleanup count = %d, %v", inside, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var outside int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations WHERE id=?`, donationID).Scan(&outside); err != nil || outside != 1 {
		t.Fatalf("rolled-back cleanup count = %d, %v", outside, err)
	}
}
