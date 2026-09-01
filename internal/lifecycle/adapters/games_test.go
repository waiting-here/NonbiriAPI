package adapters

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	fishingruntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type fakeFishingLifecycle struct {
	export        fishingruntime.UserExport
	exportErr     error
	deleteRequest [2]int64
	retention     fishingruntime.RetentionResult
}

func (owner *fakeFishingLifecycle) ExportTx(context.Context, *sql.Tx, int64, int64, int) (fishingruntime.UserExport, error) {
	return owner.export, owner.exportErr
}
func (owner *fakeFishingLifecycle) PrepareDeleteTx(_ context.Context, _ *sql.Tx, userID, now int64) error {
	owner.deleteRequest = [2]int64{userID, now}
	return nil
}
func (owner *fakeFishingLifecycle) Retain(context.Context, int64, int, time.Time) (fishingruntime.RetentionResult, error) {
	return owner.retention, nil
}

func TestFishingAdapterMapsOnlyLifecycleFields(t *testing.T) {
	next, revealed := int64(120), int64(130)
	owner := &fakeFishingLifecycle{export: fishingruntime.UserExport{
		Pending: []fishingruntime.FishingSettlementPending{{
			BatchID: "fb_AAAAAAAAAAAAAAAAAAAAAA", Bait: "worm", Count: 1,
			EntryTotal: "2.5", State: fishingruntime.PendingStateSettlement, NextAttemptAt: &next,
		}},
		Terminal: []fishingruntime.FishingTerminalExport{{
			BatchID: "fb_BBBBBBBBBBBBBBBBBBBBBB", Bait: "lure", Count: 1,
			UnitPrice: "5", EntryTotal: "5", PayoutTotal: "6", SettledAt: 125, RevealedAt: &revealed,
			Outcomes: []fishingruntime.FishingOutcome{{
				Ordinal: 0, SpeciesKey: "crucian", Tier: "regular", SizeCM: 20, Reward: "6",
			}},
		}},
		Single: &fishingruntime.FishingLeaderboardRow{Rank: "1", SpeciesKey: "boot", SizeCM: 0},
		Total:  &fishingruntime.FishingLeaderboardRow{Rank: "2", TotalCredits: "6"},
	}}
	adapter := NewFishing(owner)
	value, finalizer, err := adapter.ExportFishing(context.Background(), nil, lifecycle.ExportRequest{
		UserID: 7, DecisionNow: 140, Limit: 10,
	})
	if err != nil || finalizer != nil {
		t.Fatalf("ExportFishing finalizer=%v err=%v", finalizer, err)
	}
	if len(value.Pending) != 1 || value.Pending[0].NextAttemptAt == nil || *value.Pending[0].NextAttemptAt != next ||
		len(value.Terminal) != 1 || value.Terminal[0].RevealedAt == nil || *value.Terminal[0].RevealedAt != revealed ||
		len(value.Terminal[0].Outcomes) != 1 || value.Terminal[0].Outcomes[0].SpeciesKey != "crucian" {
		t.Fatalf("Fishing export = %#v", value)
	}
	if value.SingleBest == nil || value.SingleBest.SpeciesKey == nil || value.SingleBest.SizeCM == nil ||
		*value.SingleBest.SizeCM != 0 || value.SingleBest.TotalCredits != nil || value.RollingTotal == nil ||
		value.RollingTotal.TotalCredits == nil || *value.RollingTotal.TotalCredits != "6" {
		t.Fatalf("Fishing ranks = single %#v total %#v", value.SingleBest, value.RollingTotal)
	}
	next, revealed = 999, 999
	if *value.Pending[0].NextAttemptAt != 120 || *value.Terminal[0].RevealedAt != 130 {
		t.Fatal("Fishing pointer fields were not copied")
	}
	if _, err := adapter.PrepareDelete(context.Background(), nil, lifecycle.DeleteRequest{UserID: 7, DecisionNow: 150}); err != nil {
		t.Fatalf("PrepareDelete: %v", err)
	}
	if owner.deleteRequest != [2]int64{7, 150} {
		t.Fatalf("delete request = %v", owner.deleteRequest)
	}
	owner.retention = fishingruntime.RetentionResult{Processed: 3, More: true}
	retained, err := adapter.Retain(context.Background(), 160, 10, time.Now().Add(time.Second))
	if err != nil || retained != (lifecycle.WorkResult{Processed: 3, More: true}) {
		t.Fatalf("Retain = %#v, %v", retained, err)
	}
}

func TestFishingAdapterRejectsInvalidRankAndMapsLimit(t *testing.T) {
	owner := &fakeFishingLifecycle{export: fishingruntime.UserExport{
		Single: &fishingruntime.FishingLeaderboardRow{Rank: "1"},
	}}
	adapter := NewFishing(owner)
	if _, _, err := adapter.ExportFishing(context.Background(), nil, lifecycle.ExportRequest{}); !errors.Is(err, lifecycle.ErrInvariant) {
		t.Fatalf("invalid rank error = %v", err)
	}
	owner.export = fishingruntime.UserExport{}
	owner.exportErr = fishingruntime.ErrLifecycleResourceLimit
	if _, _, err := adapter.ExportFishing(context.Background(), nil, lifecycle.ExportRequest{}); !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("resource limit error = %v", err)
	}
}

type fakeLinkLinkLifecycle struct {
	export          linklink.UserExport
	exportFinalizer *linklink.ExportFinalizer
	exportErr       error
	deleteFinalizer *linklink.DeletionFinalizer
	retention       linklink.RetentionResult
}

func (owner *fakeLinkLinkLifecycle) ExportTx(context.Context, *sql.Tx, int64, int64, int) (linklink.UserExport, *linklink.ExportFinalizer, error) {
	return owner.export, owner.exportFinalizer, owner.exportErr
}
func (owner *fakeLinkLinkLifecycle) PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) (*linklink.DeletionFinalizer, error) {
	return owner.deleteFinalizer, nil
}
func (owner *fakeLinkLinkLifecycle) Retain(context.Context, int64, int, time.Time) (linklink.RetentionResult, error) {
	return owner.retention, nil
}

func TestLinkLinkAdapterPreservesSummaryAndFinalizers(t *testing.T) {
	score := "48"
	exportFinalizer := &linklink.ExportFinalizer{}
	deleteFinalizer := &linklink.DeletionFinalizer{}
	owner := &fakeLinkLinkLifecycle{
		exportFinalizer: exportFinalizer, deleteFinalizer: deleteFinalizer,
		export: linklink.UserExport{
			Active: &linklink.SafeActiveExport{
				SessionID: "ll_AAAAAAAAAAAAAAAAAAAAAA", Spec: "6x8", Price: "1", State: "active",
				PairsRemoved: 3, TotalPairs: 24, StartedAt: 100, Deadline: 250,
			},
			Summaries: []linklink.Summary{{
				SessionID: "ll_BBBBBBBBBBBBBBBBBBBBBB", Spec: "8x8", Price: "2",
				TerminalReason: "completed", StartedAt: 10, Deadline: 190, TerminalAt: 150,
				PairsRemoved: 32, TotalPairs: 32, Score: &score,
			}},
		},
	}
	adapter := NewLinkLink(owner)
	value, finalizer, err := adapter.ExportLinkLink(context.Background(), nil, lifecycle.ExportRequest{})
	if err != nil || finalizer != exportFinalizer || value.Active == nil || value.Active.TotalPairs != 24 ||
		len(value.Summaries) != 1 || value.Summaries[0].TotalPairs != 32 || value.Summaries[0].Score == nil {
		t.Fatalf("LinkLink export = %#v finalizer=%v err=%v", value, finalizer, err)
	}
	prepared, err := adapter.PrepareDelete(context.Background(), nil, lifecycle.DeleteRequest{})
	if err != nil || prepared != deleteFinalizer {
		t.Fatalf("LinkLink delete finalizer=%v err=%v", prepared, err)
	}
	owner.retention = linklink.RetentionResult{Processed: 2, More: true}
	retained, err := adapter.Retain(context.Background(), 1, 2, time.Now().Add(time.Second))
	if err != nil || retained != (lifecycle.WorkResult{Processed: 2, More: true}) {
		t.Fatalf("LinkLink retain=%#v err=%v", retained, err)
	}
	owner.exportErr = linklink.ErrLifecycleResourceLimit
	if _, _, err := adapter.ExportLinkLink(context.Background(), nil, lifecycle.ExportRequest{}); !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("LinkLink limit error = %v", err)
	}
}

type fakeRPSLifecycle struct {
	export          rps.UserExport
	exportFinalizer *rps.ExportFinalizer
	exportErr       error
	deleteFinalizer *rps.DeletionFinalizer
	retention       rps.RetentionResult
}

func (owner *fakeRPSLifecycle) ExportTx(context.Context, *sql.Tx, int64, int64, int) (rps.UserExport, *rps.ExportFinalizer, error) {
	return owner.export, owner.exportFinalizer, owner.exportErr
}
func (owner *fakeRPSLifecycle) PrepareDeleteTx(context.Context, *sql.Tx, int64, int64) (*rps.DeletionFinalizer, error) {
	return owner.deleteFinalizer, nil
}
func (owner *fakeRPSLifecycle) Retain(context.Context, int64, int, time.Time) (rps.RetentionResult, error) {
	return owner.retention, nil
}

func TestRPSAdapterMapsQueuePendingSummaryAndStats(t *testing.T) {
	exportFinalizer := &rps.ExportFinalizer{}
	deleteFinalizer := &rps.DeletionFinalizer{}
	owner := &fakeRPSLifecycle{
		exportFinalizer: exportFinalizer, deleteFinalizer: deleteFinalizer,
		export: rps.UserExport{
			Current: &rps.HomeState{Kind: "queue", Queue: &rps.Queue{
				ID: "rpsq_AAAAAAAAAAAAAAAAAAAAAA", Mode: "quick", State: "queued", Deadline: 200,
			}},
			Pending: &rps.PendingResult{
				SessionID: "rps_AAAAAAAAAAAAAAAAAAAAAA", Mode: "quick", TerminalReason: "quick_resolved",
				OwnSeatNo: 1, OwnInput: "5", OwnReturned: "6", OwnWalletNet: "1",
				Seats: []rps.PendingSeat{{SeatNo: 0, Result: "peer"}, {SeatNo: 1, Result: "self"}}, CreatedAt: 180,
			},
			Summaries: []rps.SummaryExport{{
				SessionID: "rps_BBBBBBBBBBBBBBBBBBBBBB", Mode: "standard",
				TerminalReason: "standard_round_limit", StartedAt: 10, TerminalAt: 170,
				OwnSeat: rps.SummarySeatExport{
					SeatNo: 2, Input: "10", Returned: "11", WalletNet: "1",
					TimeoutCount: "0", RockCount: "1", ScissorsCount: "2", PaperCount: "3",
				},
			}},
			FunStats: &rps.FunStatsExport{
				CompletedCount: "9", ProfitableCount: "4", RockCount: "3", ScissorsCount: "2", PaperCount: "1",
			},
			TutorialSeen: true,
		},
	}
	adapter := NewRPS(owner)
	value, finalizer, err := adapter.ExportRPS(context.Background(), nil, lifecycle.ExportRequest{})
	if err != nil || finalizer != exportFinalizer || value.Current == nil || value.Current.Kind != "queue" ||
		value.Current.Deadline == nil || *value.Current.Deadline != 200 || value.Pending == nil ||
		len(value.Pending.Seats) != 2 || len(value.Summaries) != 1 || value.Summaries[0].OwnSeat.PaperCount != "3" ||
		value.FunStats == nil || value.FunStats.CompletedCount != "9" || !value.TutorialSeen {
		t.Fatalf("RPS export = %#v finalizer=%v err=%v", value, finalizer, err)
	}
	prepared, err := adapter.PrepareDelete(context.Background(), nil, lifecycle.DeleteRequest{})
	if err != nil || prepared != deleteFinalizer {
		t.Fatalf("RPS delete finalizer=%v err=%v", prepared, err)
	}
	owner.retention = rps.RetentionResult{Processed: 4, More: true}
	retained, err := adapter.Retain(context.Background(), 1, 4, time.Now().Add(time.Second))
	if err != nil || retained != (lifecycle.WorkResult{Processed: 4, More: true}) {
		t.Fatalf("RPS retain=%#v err=%v", retained, err)
	}
	owner.export.Current = &rps.HomeState{Kind: "queue"}
	if _, gotFinalizer, err := adapter.ExportRPS(context.Background(), nil, lifecycle.ExportRequest{}); !errors.Is(err, lifecycle.ErrInvariant) || gotFinalizer != exportFinalizer {
		t.Fatalf("invalid RPS union finalizer=%v error=%v", gotFinalizer, err)
	}
	owner.exportErr = rps.ErrResourceLimit
	if _, _, err := adapter.ExportRPS(context.Background(), nil, lifecycle.ExportRequest{}); !errors.Is(err, lifecycle.ErrTooLarge) {
		t.Fatalf("RPS limit error = %v", err)
	}
}
