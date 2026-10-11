package claim

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestLedgerAccountingNegativeSelfRelease(t *testing.T) {
	fixture := newClaimFixture(t)
	userID := fixture.seedUser("negative-self", false)
	fixture.seedLedgerUser(userID, -750)
	fixture.useLedgerAccounting()

	request, err := fixture.service.Accept(context.Background(), AcceptInput{
		UserID: userID, Route: RouteOpenAIChat, ModelSnapshot: "public/model",
		AttemptLimit: 1,
	})
	if err != nil {
		t.Fatalf("accept zero-cost self request with negative credit: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 1, 1, 2, 4)
	fixture.requireLedgerBalanceForUser(userID, "-750")
	fixture.requireLedgerBalanceForCode(forwardReserveAccountCode, "0")

	input := CompleteRequestInput{
		RequestID: request.ID, Caller: CallerResult{Class: ResultCancelled},
		Disposition: AccountingRelease,
	}
	terminal, err := fixture.service.CompleteRequest(context.Background(), input)
	if err != nil {
		t.Fatalf("release zero-cost self request with negative credit: %v", err)
	}
	if terminal.State != RequestTerminal || terminal.AccountingDisposition != AccountingRelease {
		t.Fatalf("cancelled request = %+v", terminal)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 3, 6)
	fixture.requireLedgerBalanceForUser(userID, "-750")
	fixture.requireLedgerBalanceForCode(forwardReserveAccountCode, "0")

	if _, err := fixture.service.CompleteRequest(context.Background(), input); err != nil {
		t.Fatalf("replay zero-cost self release: %v", err)
	}
	fixture.requireRealLedgerState(request.ID, 0, 0, 3, 6)
	fixture.requireLedgerBalanceForUser(userID, "-750")
}

func TestLedgerAccountingPositiveReserveStillRequiresCredit(t *testing.T) {
	for _, balance := range []int64{-750, 50} {
		for _, route := range []RouteKind{RouteOpenAIChat, RouteCharityChat} {
			t.Run(string(route)+"/"+ledger.AmountFromMilli(balance).Decimal(), func(t *testing.T) {
				fixture := newClaimFixture(t)
				userID := fixture.seedUser("insufficient-reserve", false)
				fixture.seedLedgerUser(userID, balance)
				fixture.useLedgerAccounting()
				input := AcceptInput{
					UserID: userID, Route: route, ModelSnapshot: "public/model",
					AttemptLimit: 1, ReservedMilli: 100,
				}
				if route.IsCharity() {
					decisionNow := fixture.clock.Load()
					input.ModelSnapshot = "[公益]provider/model"
					input.CharityModelID = 1
					input.CharityDecisionNow = &decisionNow
				}
				if _, err := fixture.service.Accept(context.Background(), input); !errors.Is(err, ledger.ErrInsufficientBalance) {
					t.Fatalf("positive reserve error = %v, want insufficient balance", err)
				}
				var requests int
				if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM logical_requests`).Scan(&requests); err != nil {
					t.Fatal(err)
				}
				if requests != 0 {
					t.Fatalf("failed reserve left %d requests", requests)
				}
				fixture.requireRealLedgerState("", 0, 0, 1, 2)
				fixture.requireLedgerBalanceForUser(userID, ledger.AmountFromMilli(balance).Decimal())
			})
		}
	}
}
