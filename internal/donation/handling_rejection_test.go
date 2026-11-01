package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestPendingReviewClosesHandlingForEveryReviewer(t *testing.T) {
	for _, level := range []int64{0, 5, 6} {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			e, actor, d := agentReviewFixture(t)
			if level != 0 {
				actor.ID = e.seedUser(t, "pending-reviewer", &level, false)
				actor.Role = fmt.Sprintf("level%d", level)
				qualifyStandardDonation(t, e, d.ID)
			}
			op := agentReviewOperation(t, e, d, "reject")
			tx, err := e.store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := e.service.AgentApply(t.Context(), tx, actor, op, allowAgent); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var state, reason string
			var revision, closedAt int64
			if err := e.store.DB().QueryRow(`SELECT state,revision,closed_at,closed_reason FROM donation_handling WHERE donation_id=?`, d.ID).Scan(&state, &revision, &closedAt, &reason); err != nil {
				t.Fatal(err)
			}
			if state != "closed" || revision != 2 || closedAt != e.clock.Load() || reason != "rejected" {
				t.Fatalf("handling=%s revision=%d closed_at=%d reason=%s", state, revision, closedAt, reason)
			}
		})
	}
}

func TestTerminalHandlingCompatibilityAcrossManagementReads(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	ctx := context.Background()
	level := int64(6)
	steward := e.seedUser(t, "handling-reader", &level, false)
	id := parseTestID(t, d.ID)
	if _, err := e.store.DB().Exec(`UPDATE donations SET status='rejected',terminal_at=? WHERE id=?`, e.clock.Load(), id); err != nil {
		t.Fatal(err)
	}
	check := func(h DonationHandling) {
		t.Helper()
		if h.State != "closed" || h.Revision != "1" || h.ClosedAt != nil || h.ClosedReason == nil || *h.ClosedReason != "rejected" {
			t.Fatalf("historical handling=%+v", h)
		}
	}
	admin, err := e.service.GetAdmin(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	check(admin.Handling)
	view, err := e.service.GetSteward(ctx, steward, id)
	if err != nil {
		t.Fatal(err)
	}
	check(view.Handling)
	page, err := e.service.DonationsAdminPage(ctx, ManagementFilter{Handling: "closed"}, pagination.Default())
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("admin page=%+v, err=%v", page, err)
	}
	check(page.Data[0].Handling)
	stewardPage, err := e.service.DonationsStewardPage(ctx, steward, ManagementFilter{Handling: "closed"}, pagination.Default())
	if err != nil || len(stewardPage.Data) != 1 {
		t.Fatalf("steward page=%+v, err=%v", stewardPage, err)
	}
	check(stewardPage.Data[0].Handling)
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		badge, err := e.service.badge(ctx, role, steward)
		if err != nil || badge.PendingCount != "0" {
			t.Fatalf("badge=%+v, err=%v", badge, err)
		}
	}
	tx, err := e.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	header, err := logicalHeaderTx(ctx, tx, id, e.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	check(header.Handling)
	op := agentplan.Operation{Kind: "donation.handle", TargetID: d.ID, Input: json.RawMessage(`{"expected_revision":"1"}`)}
	if _, err := e.service.AgentPreview(ctx, tx, actor, op, allowAgent); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal preview=%v", err)
	}
	historical := admin
	historical.Handling = DonationHandling{State: "pending", Revision: "1"}
	body, err := json.Marshal(historical)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []reviewerRole{reviewerAdmin, reviewerSteward} {
		replay, err := replayRoleDonation(ctx, tx, idempotency.Decision{Kind: idempotency.Replay, HTTPStatus: http.StatusOK, ResponseBody: body}, role, steward, id)
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Handling DonationHandling `json:"handling"`
		}
		if err := json.Unmarshal(replay.body, &response); err != nil {
			t.Fatal(err)
		}
		check(response.Handling)
	}
	tx.Rollback()
	if _, err := e.service.ProcessAdmin(ctx, id, donationMutation(t, 'R', http.MethodPost, routeAdminProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"}), 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal processing=%v", err)
	}
	var state string
	var revision int64
	if err := e.store.DB().QueryRow(`SELECT state,revision FROM donation_handling WHERE donation_id=?`, id).Scan(&state, &revision); err != nil || state != "pending" || revision != 1 {
		t.Fatalf("read changed historical event: %s/%d, err=%v", state, revision, err)
	}
}

func TestAgentHandlingPreviewRejectsLogicalExpiry(t *testing.T) {
	e, actor, d := agentReviewFixture(t)
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE donation_id=?`, e.clock.Load(), d.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	op := agentplan.Operation{Kind: "donation.handle", TargetID: d.ID, Input: json.RawMessage(`{"expected_revision":"1"}`)}
	if _, err := e.service.AgentPreview(t.Context(), tx, actor, op, allowAgent); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired preview=%v", err)
	}
}
