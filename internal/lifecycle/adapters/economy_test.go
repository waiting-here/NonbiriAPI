package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeActivityOwner struct {
	export                  activities.UserExport
	exportErr               error
	exportUserID            int64
	exportLimit             int
	deleteUserID, deleteNow int64
}

func (owner *fakeActivityOwner) ExportUserTx(
	_ context.Context,
	_ *sql.Tx,
	userID int64,
	limit int,
) (activities.UserExport, error) {
	owner.exportUserID = userID
	owner.exportLimit = limit
	return owner.export, owner.exportErr
}

func (owner *fakeActivityOwner) PrepareUserDeletion(
	_ context.Context,
	_ *sql.Tx,
	userID, decisionNow int64,
) error {
	owner.deleteUserID = userID
	owner.deleteNow = decisionNow
	return nil
}

type fakeDonationOwner struct {
	export                  []donation.ExportDonation
	exportErr               error
	exportUserID            int64
	exportNow               int64
	exportLimit             int
	deleteUserID, deleteNow int64
	cleanupNow              int64
	cleanupLimit            int
	cleanupProcessed        int
	cleanupErr              error
	cleanupDeadline         time.Time
}

func (owner *fakeDonationOwner) ExportUserTx(
	_ context.Context,
	_ *sql.Tx,
	userID, decisionNow int64,
	limit int,
) ([]donation.ExportDonation, error) {
	owner.exportUserID = userID
	owner.exportNow = decisionNow
	owner.exportLimit = limit
	return owner.export, owner.exportErr
}

func (owner *fakeDonationOwner) PrepareAccountDeletion(
	_ context.Context,
	_ *sql.Tx,
	userID, decisionNow int64,
) error {
	owner.deleteUserID = userID
	owner.deleteNow = decisionNow
	return nil
}

func (owner *fakeDonationOwner) Cleanup(ctx context.Context, decisionNow int64, limit int) (int, error) {
	owner.cleanupNow = decisionNow
	owner.cleanupLimit = limit
	owner.cleanupDeadline, _ = ctx.Deadline()
	return owner.cleanupProcessed, owner.cleanupErr
}

type fakeCharityOwner struct {
	export           charity.ConsumerExport
	exportErr        error
	exportUserID     int64
	exportNow        int64
	exportLimit      int
	cleanupNow       int64
	cleanupLimit     int
	cleanupProcessed int
	cleanupErr       error
	cleanupDeadline  time.Time
}

func (owner *fakeCharityOwner) ExportConsumerTx(
	_ context.Context,
	_ *sql.Tx,
	userID, decisionNow int64,
	limit int,
) (charity.ConsumerExport, error) {
	owner.exportUserID = userID
	owner.exportNow = decisionNow
	owner.exportLimit = limit
	return owner.export, owner.exportErr
}

func (owner *fakeCharityOwner) Cleanup(ctx context.Context, decisionNow int64, limit int) (int, error) {
	owner.cleanupNow = decisionNow
	owner.cleanupLimit = limit
	owner.cleanupDeadline, _ = ctx.Deadline()
	return owner.cleanupProcessed, owner.cleanupErr
}

func TestEconomyAdaptersMapClosedExportDTOs(t *testing.T) {
	ctx := context.Background()
	request := lifecycle.ExportRequest{UserID: 41, DecisionNow: 52, Limit: 63}
	unpaidReason := "wallet_missing"
	activityOwner := &fakeActivityOwner{export: activities.UserExport{
		WelfareClaims: []activities.WelfareClaimExport{{
			SiteDay: "2026-09-01", Threshold: "1", Cap: "2", Awarded: "0.5", CreatedAt: 70,
		}},
		Thursday: []activities.ThursdayParticipantExport{{
			PeriodID: "period", PeriodKey: "2026-W36", Count: "3", Contributed: "0.3",
			Eligible: true, Settled: true, Payout: "0.9", UnpaidReason: &unpaidReason,
			CreatedAt: 71, UpdatedAt: 72,
		}},
	}}
	welfare, thursday, err := NewActivity(activityOwner).ExportActivities(ctx, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if activityOwner.exportUserID != request.UserID || activityOwner.exportLimit != request.Limit {
		t.Fatalf("activity export request = (%d,%d)", activityOwner.exportUserID, activityOwner.exportLimit)
	}
	if !reflect.DeepEqual(welfare, []lifecycle.WelfareExport{{
		SiteDay: "2026-09-01", Threshold: "1", Cap: "2", Awarded: "0.5", CreatedAt: 70,
	}}) {
		t.Fatalf("welfare export = %+v", welfare)
	}
	if !reflect.DeepEqual(thursday, []lifecycle.ThursdayExport{{
		PeriodID: "period", PeriodKey: "2026-W36", Count: "3", Contributed: "0.3",
		Eligible: true, Settled: true, Payout: "0.9", UnpaidReason: &unpaidReason,
		CreatedAt: 71, UpdatedAt: 72,
	}}) {
		t.Fatalf("Thursday export = %+v", thursday)
	}

	endpointKeyID := "endpoint-key-id"
	priceLimit, callsLimit, tokensLimit := "10.5", "20", "3000"
	endedReason := "expired"
	expiresAt := int64(99)
	donationOwner := &fakeDonationOwner{export: []donation.ExportDonation{{
		ID: "donation", Status: "approved", Description: "description",
		ReviewResult: &donation.ReviewResult{Decision: "approve", Reason: "accepted", ReviewedAt: 80},
		ExpiresAt:    &expiresAt,
		Keys: []donation.DonationKey{{
			ID: "donation-key", EndpointKeyID: &endpointKeyID, DisplayHead: "sk-a", DisplayTail: "tail",
			SafeSource:      donation.SafeSource{BaseURL: "https://example.invalid", ConnectorType: "openai"},
			PhysicalEnabled: true, CharityState: "enabled",
			Limits: donation.DonationLimits{Price: &priceLimit, Calls: &callsLimit, Tokens: &tokensLimit},
			Usage: donation.DonationUsage{
				PriceUsed: "1", PriceInflight: "2", CallsUsed: "3", CallsInflight: "4",
				TokensUsed: "5", TokensInflight: "6",
			},
			TokenReserve: 7,
			Streak: donation.DonationStreak{
				Generation: "8", Count: "9", FailureDisabled: true,
			},
			SafeNote: "HOSTILE_SAFE_NOTE", EndedReason: &endedReason,
		}},
		CreatedAt: 81, UpdatedAt: 82,
	}}}
	donations, err := NewDonation(donationOwner).ExportDonations(ctx, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if donationOwner.exportUserID != request.UserID || donationOwner.exportNow != request.DecisionNow ||
		donationOwner.exportLimit != request.Limit {
		t.Fatalf("donation export request = (%d,%d,%d)", donationOwner.exportUserID, donationOwner.exportNow, donationOwner.exportLimit)
	}
	wantDonations := []lifecycle.DonationExport{{
		ID: "donation", Status: "approved", Description: "description",
		ReviewResult: &lifecycle.DonationReviewExport{Decision: "approve", Reason: "accepted", ReviewedAt: 80},
		ExpiresAt:    &expiresAt,
		Keys: []lifecycle.DonationKeyExport{{
			ID: "donation-key", EndpointKeyID: &endpointKeyID, DisplayHead: "sk-a", DisplayTail: "tail",
			BaseURL: "https://example.invalid", ConnectorType: "openai", PhysicalEnabled: true,
			CharityState: "enabled",
			Limits:       lifecycle.DonationLimitsExport{Price: &priceLimit, Calls: &callsLimit, Tokens: &tokensLimit},
			Usage: lifecycle.DonationUsageExport{
				PriceUsed: "1", PriceInflight: "2", CallsUsed: "3", CallsInflight: "4",
				TokensUsed: "5", TokensInflight: "6",
			},
			TokenReserve: 7,
			Streak: lifecycle.DonationStreakExport{
				Generation: "8", Count: "9", FailureDisabled: true,
			},
			EndedReason: &endedReason,
		}},
		CreatedAt: 81, UpdatedAt: 82,
	}}
	if !reflect.DeepEqual(donations, wantDonations) {
		t.Fatalf("donation export = %+v, want %+v", donations, wantDonations)
	}
	encoded, err := json.Marshal(donations)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "safe_note") || strings.Contains(string(encoded), "HOSTILE_SAFE_NOTE") {
		t.Fatalf("donation export leaked safe_note: %s", encoded)
	}

	charityOwner := &fakeCharityOwner{export: charity.ConsumerExport{
		RequestCount: "1", OriginalCharge: "2.5", Charged: "2", Saved: "0.5", DonorReward: "1.25",
		UncachedInput: "11", CacheWriteInput: "12", CacheReadInput: "13", Output: "14", UsageUnknownCount: "1",
	}}
	charityExport, err := NewCharity(charityOwner).ExportCharity(ctx, nil, request)
	if err != nil {
		t.Fatal(err)
	}
	if charityOwner.exportUserID != request.UserID || charityOwner.exportNow != request.DecisionNow ||
		charityOwner.exportLimit != request.Limit {
		t.Fatalf("charity export request = (%d,%d,%d)", charityOwner.exportUserID, charityOwner.exportNow, charityOwner.exportLimit)
	}
	wantCharity := lifecycle.CharityExport{
		RequestCount: "1", OriginalCharge: "2.5", Charged: "2", Saved: "0.5", DonorReward: "1.25",
		UncachedInput: "11", CacheWriteInput: "12", CacheReadInput: "13", Output: "14", UsageUnknownCount: "1",
	}
	if charityExport != wantCharity {
		t.Fatalf("charity export = %+v, want %+v", charityExport, wantCharity)
	}
}

func TestEconomyAdaptersDelegateDeleteAndRetention(t *testing.T) {
	ctx := context.Background()
	request := lifecycle.DeleteRequest{UserID: 101, DecisionNow: 202}
	activityOwner := &fakeActivityOwner{}
	finalizer, err := NewActivity(activityOwner).PrepareDelete(ctx, nil, request)
	if err != nil || finalizer != nil {
		t.Fatalf("activity delete = (%v,%v)", finalizer, err)
	}
	if activityOwner.deleteUserID != request.UserID || activityOwner.deleteNow != request.DecisionNow {
		t.Fatalf("activity delete request = (%d,%d)", activityOwner.deleteUserID, activityOwner.deleteNow)
	}

	donationOwner := &fakeDonationOwner{cleanupProcessed: 3}
	finalizer, err = NewDonation(donationOwner).PrepareDelete(ctx, nil, request)
	if err != nil || finalizer != nil {
		t.Fatalf("donation delete = (%v,%v)", finalizer, err)
	}
	if donationOwner.deleteUserID != request.UserID || donationOwner.deleteNow != request.DecisionNow {
		t.Fatalf("donation delete request = (%d,%d)", donationOwner.deleteUserID, donationOwner.deleteNow)
	}
	donationDeadline := time.Now().Add(time.Second)
	donationWork, err := NewDonation(donationOwner).Retain(ctx, 303, 3, donationDeadline)
	if err != nil || donationWork != (lifecycle.WorkResult{Processed: 3, More: true}) {
		t.Fatalf("donation retention = (%+v,%v)", donationWork, err)
	}
	if donationOwner.cleanupNow != 303 || donationOwner.cleanupLimit != 3 {
		t.Fatalf("donation cleanup request = (%d,%d)", donationOwner.cleanupNow, donationOwner.cleanupLimit)
	}
	if !donationOwner.cleanupDeadline.Equal(donationDeadline) {
		t.Fatalf("donation cleanup deadline = %v, want %v", donationOwner.cleanupDeadline, donationDeadline)
	}

	charityOwner := &fakeCharityOwner{cleanupProcessed: 2}
	charityDeadline := time.Now().Add(2 * time.Second)
	charityWork, err := NewCharity(charityOwner).Retain(ctx, 404, 3, charityDeadline)
	if err != nil || charityWork != (lifecycle.WorkResult{Processed: 2, More: false}) {
		t.Fatalf("charity retention = (%+v,%v)", charityWork, err)
	}
	if charityOwner.cleanupNow != 404 || charityOwner.cleanupLimit != 3 {
		t.Fatalf("charity cleanup request = (%d,%d)", charityOwner.cleanupNow, charityOwner.cleanupLimit)
	}
	if !charityOwner.cleanupDeadline.Equal(charityDeadline) {
		t.Fatalf("charity cleanup deadline = %v, want %v", charityOwner.cleanupDeadline, charityDeadline)
	}
}

func TestEconomyAdaptersTranslateResourceLimits(t *testing.T) {
	ctx := context.Background()
	request := lifecycle.ExportRequest{UserID: 1, DecisionNow: 2, Limit: 3}
	_, _, err := NewActivity(&fakeActivityOwner{exportErr: activities.ErrResourceLimit}).ExportActivities(ctx, nil, request)
	if !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("activity error = %v", err)
	}
	_, err = NewDonation(&fakeDonationOwner{exportErr: donation.ErrResourceLimit}).ExportDonations(ctx, nil, request)
	if !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("donation error = %v", err)
	}
	_, err = NewCharity(&fakeCharityOwner{exportErr: charity.ErrResourceLimit}).ExportCharity(ctx, nil, request)
	if !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("charity error = %v", err)
	}
}
