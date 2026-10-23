package adminalerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/returnmarker"
)

func returnedEvent(user int64) returnmarker.Event {
	return returnmarker.Event{UserID: user, ObservedAt: alertTestNow, Ref: returnDetectedPrefix + strings.Repeat("a", 64),
		RequestID: "req_" + strings.Repeat("A", 22),
		Evidence:  returnmarker.Evidence{Source: returnmarker.Source{RequestID: "req_" + strings.Repeat("B", 21) + "A", EndpointID: 12, EndpointKeyID: 13, DonationKeyID: 14}, IssuedAt: alertTestNow - 1}}
}

func TestReturnDetectedProjectionDedupAndResolution(t *testing.T) {
	f := newAlertTestEnvironment(t)
	event := returnedEvent(f.adminID)
	for range 2 {
		if err := f.repository.RecordReturnDetected(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.repository.List(t.Context(), f.adminID, ListQuery{})
	if err != nil || len(page.Data) != 1 {
		t.Fatal("return evidence was not deduplicated", err)
	}
	alert := page.Data[0]
	if alert.Kind != KindForwardError || alert.Subtype != "return_detected" || alert.Resolved || alert.SubjectUserID == nil {
		t.Fatal("wrong alert projection", alert)
	}
	var id int64
	if err := f.store.DB().QueryRow("SELECT id FROM admin_alerts WHERE ref=?", event.Ref).Scan(&id); err != nil {
		t.Fatal(err)
	}
	detail, err := f.repository.GetDetail(t.Context(), f.adminID, id)
	if err != nil || detail.ContextVersion != 1 || len(detail.Targets) != 5 || len(detail.OccurredFacts) != 4 {
		t.Fatal("wrong detail projection", detail, err)
	}
	for _, target := range detail.Targets {
		if target.Available {
			t.Fatal("invented live target")
		}
	}
	wire, _ := json.Marshal(detail)
	if strings.Contains(string(wire), "provider.example") || strings.Contains(string(wire), "nonbiri-request-origin") {
		t.Fatal("private marker data in alert")
	}
	if _, err := f.repository.SetResolved(t.Context(), f.adminID, id, true); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.RecordReturnDetected(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM admin_alerts").Scan(&count); err != nil || count != 1 {
		t.Fatal("resolution bypassed dedup", count, err)
	}
}

func TestReturnDetectedDeletionOrdersDoNotRestoreSubject(t *testing.T) {
	for _, insertFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "alert_before_delete", false: "delete_before_alert"}[insertFirst], func(t *testing.T) {
			f := newAlertTestEnvironment(t)
			result, err := f.store.DB().Exec(`INSERT INTO users(discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,lang,created_at,updated_at)
 SELECT 'return-subject','return subject',0,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,lang,created_at,updated_at FROM users WHERE id=?`, f.adminID)
			if err != nil {
				t.Fatal(err)
			}
			user, err := result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			event := returnedEvent(user)
			if insertFirst {
				if err := f.repository.RecordReturnDetected(t.Context(), event); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := f.store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if err := anonymizeReturnAlertsTx(t.Context(), tx, user); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(t.Context(), "DELETE FROM users WHERE id=?", user); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := f.repository.RecordReturnDetected(t.Context(), event); err != nil {
				t.Fatal(err)
			}
			var linked int
			if err := f.store.DB().QueryRow("SELECT COUNT(*) FROM admin_alerts WHERE subject_user_id=?", user).Scan(&linked); err != nil || linked != 0 {
				t.Fatal("restored deleted subject", err)
			}
			var subject sql.NullInt64
			err = f.store.DB().QueryRow("SELECT subject_user_id FROM admin_alerts WHERE ref=?", event.Ref).Scan(&subject)
			if insertFirst && (err != nil || subject.Valid) {
				t.Fatal("existing alert not anonymized", err)
			}
			if !insertFirst && err != sql.ErrNoRows {
				t.Fatal("late event created alert after deletion", err)
			}
		})
	}
}

func TestReturnDetectedHonorsCanceledWrite(t *testing.T) {
	f := newAlertTestEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.repository.RecordReturnDetected(ctx, returnedEvent(f.adminID)); err == nil {
		t.Fatal("ignored cancellation")
	}
}
