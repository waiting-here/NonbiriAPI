package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type donationHeldReadStub struct {
	allow bool
	calls int
	id    int64
}

func (stub *donationHeldReadStub) AuthorizeHeldDonationRead(
	_ context.Context,
	_ *sql.Tx,
	donationID int64,
	_ int64,
) (bool, error) {
	stub.calls++
	stub.id = donationID
	return stub.allow, nil
}

func TestDonationOrdinaryCutoffAndKnownIDHeldRead(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "held-read-donation-owner", nil, false)
	_, endpointKeyID := environment.seedEndpointKey(t, owner, 'j')
	donation := environment.createDonation(t, owner, endpointKeyID)
	donationID := parseTestID(t, donation.ID)
	mutation := donationMutation(t, 'J', http.MethodPost, routeWithdraw, []int64{donationID},
		map[string]any{"expected_revision": 1})
	if _, err := environment.service.Withdraw(context.Background(), owner, donationID, mutation,
		RevisionInput{ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	environment.clock.Store(environment.clock.Load() + terminalRetention)

	hook := &donationHeldReadStub{allow: true}
	if err := environment.service.AttachAdminHeldReadAuthorizer(hook); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.GetOwner(context.Background(), owner, donationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner read at retention boundary = %v, want not found", err)
	}
	ownerItems, _, err := environment.service.ListOwner(context.Background(), owner, 0, 10)
	if err != nil || len(ownerItems) != 0 {
		t.Fatalf("owner list at retention boundary = %d, %v", len(ownerItems), err)
	}
	adminItems, _, err := environment.service.ListAdmin(context.Background(), "", 0, 10)
	if err != nil || len(adminItems) != 0 {
		t.Fatalf("admin ordinary list at retention boundary = %d, %v", len(adminItems), err)
	}
	if _, err := environment.service.GetAdmin(context.Background(), donationID); err != nil {
		t.Fatalf("active held known-ID read = %v", err)
	}
	if hook.calls != 1 || hook.id != donationID {
		t.Fatalf("held read hook calls=%d id=%d", hook.calls, hook.id)
	}
	hook.allow = false
	if _, err := environment.service.GetAdmin(context.Background(), donationID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unheld known-ID read at retention boundary = %v, want not found", err)
	}
}

func insertDonationLegalHold(
	t *testing.T,
	database *sql.DB,
	donationID, adminUserID, createdAt int64,
) {
	t.Helper()
	holdID, err := db.GenerateOpaqueID("lgh_")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO legal_holds(
id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,'donation',?,'active',1,'donation hold',?,?,?)`,
		holdID, fmt.Sprintf("%d", donationID), adminUserID, createdAt, createdAt+3600); err != nil {
		t.Fatalf("insert donation legal hold: %v", err)
	}
}

func TestHeldDonationDeadlineMarkerAggregateAndDeidentification(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "held-donation-owner", nil, false)
	admin := environment.seedUser(t, "", nil, true)
	_, endpointKeyID := environment.seedEndpointKey(t, owner, 'h')
	donation := environment.createDonation(t, owner, endpointKeyID)
	donationID := parseTestID(t, donation.ID)
	donationKeyID := parseTestID(t, donation.Keys[0].ID)
	mutation := donationMutation(t, 'H', http.MethodPost, routeWithdraw, []int64{donationID},
		map[string]any{"expected_revision": 1})
	if _, err := environment.service.Withdraw(context.Background(), owner, donationID, mutation,
		RevisionInput{ExpectedRevision: 1}); err != nil {
		t.Fatalf("withdraw held donation: %v", err)
	}
	terminalAt := environment.clock.Load()
	deadline := terminalAt + terminalRetention
	insertDonationLegalHold(t, environment.store.DB(), donationID, admin, deadline-10)

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, delta := range []int64{-1, 0, 1} {
		decisionNow := deadline + delta
		state, err := environment.service.InspectForCreate(
			context.Background(), tx, donation.ID, decisionNow,
		)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("inspect donation at deadline%+d: %v", delta, err)
		}
		if !state.Exists || state.OrdinaryDeadline != deadline || state.LegalHoldConsumed {
			_ = tx.Rollback()
			t.Fatalf("donation state at deadline%+d = %+v", delta, state)
		}
		items, err := environment.service.ExportUserTx(context.Background(), tx, owner, decisionNow, 10)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("ordinary donation export at deadline%+d: %v", delta, err)
		}
		want := 0
		if delta < 0 {
			want = 1
		}
		if len(items) != want {
			_ = tx.Rollback()
			t.Fatalf("ordinary donation export at deadline%+d has %d rows, want %d", delta, len(items), want)
		}
	}
	if err := environment.service.ConsumeMarker(context.Background(), tx, donation.ID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := environment.service.ConsumeMarker(context.Background(), tx, donation.ID); !errors.Is(err, ErrConflict) {
		_ = tx.Rollback()
		t.Fatalf("second donation marker consumption = %v, want conflict", err)
	}
	var reviewID int64
	if err := tx.QueryRow(`SELECT id FROM donation_reviews WHERE donation_id=? ORDER BY id LIMIT 1`, donationID).Scan(&reviewID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM donation_keys WHERE id=?`, donationKeyID); err == nil {
		_ = tx.Rollback()
		t.Fatal("active donation hold allowed key deletion")
	}
	if _, err := tx.Exec(`DELETE FROM donation_reviews WHERE id=?`, reviewID); err == nil {
		_ = tx.Rollback()
		t.Fatal("active donation hold allowed review deletion")
	}
	if err := environment.service.PrepareAccountDeletion(context.Background(), tx, owner, deadline); err != nil {
		_ = tx.Rollback()
		t.Fatalf("deidentify held donation: %v", err)
	}
	exists, err := environment.service.ReadHeld(context.Background(), tx, donation.ID, deadline)
	if err != nil || !exists {
		_ = tx.Rollback()
		t.Fatalf("read deidentified held donation = %v, %v", exists, err)
	}
	exists, err = environment.service.ReadHeld(context.Background(), tx, "9223372036854775807", deadline)
	if err != nil || exists {
		_ = tx.Rollback()
		t.Fatalf("read missing held donation = %v, %v", exists, err)
	}
	var projectedUser sql.NullInt64
	var keyCount, reviewCount, consumed int
	if err := tx.QueryRow(`SELECT user_id,legal_hold_consumed FROM donations WHERE id=?`, donationID).Scan(
		&projectedUser, &consumed,
	); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM donation_keys WHERE donation_id=?`, donationID).Scan(&keyCount); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=?`, donationID).Scan(&reviewCount); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if projectedUser.Valid || consumed != 1 || keyCount != 1 || reviewCount < 1 {
		_ = tx.Rollback()
		t.Fatalf("held donation user=%+v consumed=%d keys=%d reviews=%d",
			projectedUser, consumed, keyCount, reviewCount)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestHeldDonationInspectionConvergesDueApprovalInCallerTransaction(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "due-held-donation-owner", nil, false)
	environment.seedUser(t, "", nil, true)
	_, endpointKeyID := environment.seedEndpointKey(t, owner, 'i')
	donation := environment.createDonation(t, owner, endpointKeyID)
	donationID := parseTestID(t, donation.ID)
	donationKeyID := parseTestID(t, donation.Keys[0].ID)
	expiresAt := environment.clock.Load() + 100
	mutation := donationMutation(t, 'I', http.MethodPost, routeAdminReview, []int64{donationID},
		map[string]any{"decision": "approve"})
	if _, err := environment.service.ReviewAdmin(context.Background(), mutation, donationID, ReviewInput{
		Decision: "approve", ExpectedRevision: 1, Reason: "approved for expiry",
		ExpiresAt: &expiresAt, KeySettings: []KeySetting{{DonationKeyID: donationKeyID, Enabled: true}},
	}); err != nil {
		t.Fatalf("approve donation: %v", err)
	}

	tx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := environment.service.InspectForCreate(context.Background(), tx, donation.ID, expiresAt)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if !state.Exists || state.OrdinaryDeadline != expiresAt+terminalRetention || state.LegalHoldConsumed {
		_ = tx.Rollback()
		t.Fatalf("due approved state = %+v", state)
	}
	var status string
	var terminalAt int64
	var expiryReviews int
	if err := tx.QueryRow(`SELECT status,terminal_at FROM donations WHERE id=?`, donationID).Scan(&status, &terminalAt); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=? AND action='expire'`, donationID).Scan(&expiryReviews); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if status != "expired" || terminalAt != expiresAt || expiryReviews != 1 {
		_ = tx.Rollback()
		t.Fatalf("transactional expiry status=%q terminal=%d reviews=%d", status, terminalAt, expiryReviews)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var committedStatus string
	var committedTerminal sql.NullInt64
	if err := environment.store.DB().QueryRow(`SELECT status,terminal_at FROM donations WHERE id=?`, donationID).Scan(
		&committedStatus, &committedTerminal,
	); err != nil {
		t.Fatal(err)
	}
	if committedStatus != "approved" || committedTerminal.Valid {
		t.Fatalf("inspection escaped caller rollback: status=%q terminal=%+v", committedStatus, committedTerminal)
	}
}
