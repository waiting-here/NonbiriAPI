package activities

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestThursdayLifecycleRecoveryNoWorkAndInputBounds(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_000_000)
	service := newThursdayRecoveryService(t, fixture)
	deadline := time.Now().Add(time.Minute)

	result, err := service.RecoverThursday(context.Background(), fixture.clock.Load(), SettlementBatchSize, deadline)
	if err != nil || result != (ThursdayRecoveryResult{}) {
		t.Fatalf("no work result=%+v err=%v", result, err)
	}

	tests := []struct {
		name     string
		ctx      context.Context
		now      int64
		limit    int
		deadline time.Time
	}{
		{name: "nil context", now: 1, limit: 1, deadline: deadline},
		{name: "negative decision time", ctx: context.Background(), now: -1, limit: 1, deadline: deadline},
		{name: "decision time overflow", ctx: context.Background(), now: maxUnixSecond + 1, limit: 1, deadline: deadline},
		{name: "zero limit", ctx: context.Background(), now: 1, deadline: deadline},
		{name: "oversized limit", ctx: context.Background(), now: 1, limit: SettlementBatchSize + 1, deadline: deadline},
		{name: "zero deadline", ctx: context.Background(), now: 1, limit: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.RecoverThursday(test.ctx, test.now, test.limit, test.deadline)
			if !errors.Is(err, ErrInvalidRequest) || result != (ThursdayRecoveryResult{}) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}

	var nilService *Service
	if result, err := nilService.RecoverThursday(context.Background(), 1, 1, deadline); !errors.Is(err, ErrInvalidRequest) || result != (ThursdayRecoveryResult{}) {
		t.Fatalf("nil service result=%+v err=%v", result, err)
	}
}

func TestThursdayLifecycleRecoveryUsesFrozenTimeForConfiguredAndOpen(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		opensAt := beijingThursday(2027, 5, 6)
		fixture := newActivityFixture(t, opensAt-1)
		service := newThursdayRecoveryService(t, fixture)
		periodID := createThursdayRecoveryPeriod(t, fixture, opensAt, "configured")
		closesAt := opensAt + 86400

		fixture.clock.Store(closesAt + 3600)
		result, err := service.RecoverThursday(context.Background(), closesAt-1, 1, time.Now().Add(time.Minute))
		if err != nil || result != (ThursdayRecoveryResult{}) || fixture.period(periodID).state != PeriodStateConfigured {
			t.Fatalf("pre-deadline result=%+v state=%s err=%v", result, fixture.period(periodID).state, err)
		}
		result, err = service.RecoverThursday(context.Background(), closesAt, 1, time.Now().Add(time.Minute))
		if err != nil || result != (ThursdayRecoveryResult{Processed: 1, More: true}) || fixture.period(periodID).state != PeriodStateSettling {
			t.Fatalf("freeze result=%+v state=%s err=%v", result, fixture.period(periodID).state, err)
		}
		result, err = service.RecoverThursday(context.Background(), closesAt, 1, time.Now().Add(time.Minute))
		if err != nil || result != (ThursdayRecoveryResult{Processed: 1}) || fixture.period(periodID).state != PeriodStateSettled {
			t.Fatalf("empty finalization result=%+v state=%s err=%v", result, fixture.period(periodID).state, err)
		}
	})

	t.Run("open", func(t *testing.T) {
		opensAt := beijingThursday(2027, 5, 13)
		fixture := newActivityFixture(t, opensAt-1)
		service := newThursdayRecoveryService(t, fixture)
		periodID := createThursdayRecoveryPeriod(t, fixture, opensAt, "open")
		fixture.setActivityConfig(true, false, true, 0, 0)
		userID, _ := fixture.seedUser("open-recovery", false)
		fixture.fundUser(userID, 1)
		fixture.clock.Store(opensAt)
		period := fixture.period(periodID)
		if _, _, err := fixture.repository.ContributeThursday(
			context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"open": true}),
			ThursdayContributionInput{PeriodID: periodID, ExpectedRevision: period.revision},
		); err != nil {
			t.Fatal(err)
		}
		if fixture.period(periodID).state != PeriodStateOpen {
			t.Fatalf("period was not opened: %+v", fixture.period(periodID))
		}

		closesAt := opensAt + 86400
		result, err := service.RecoverThursday(context.Background(), closesAt, 1, time.Now().Add(time.Minute))
		if err != nil || result != (ThursdayRecoveryResult{Processed: 1, More: true}) {
			t.Fatalf("open freeze result=%+v err=%v", result, err)
		}
		result, err = service.RecoverThursday(context.Background(), closesAt, 1, time.Now().Add(time.Minute))
		if err != nil || result != (ThursdayRecoveryResult{Processed: 1}) || fixture.period(periodID).state != PeriodStateSettled {
			t.Fatalf("open settlement result=%+v state=%s err=%v", result, fixture.period(periodID).state, err)
		}
	})
}

func TestThursdayLifecycleRecoveryCheckpointBatchBoundaries(t *testing.T) {
	for _, test := range []struct {
		name         string
		participants int
		first        ThursdayRecoveryResult
		second       *ThursdayRecoveryResult
	}{
		{name: "exact limit", participants: 2, first: ThursdayRecoveryResult{Processed: 2}},
		{name: "one over limit", participants: 3, first: ThursdayRecoveryResult{Processed: 2, More: true}, second: &ThursdayRecoveryResult{Processed: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, service, periodID, closesAt := seedThursdayRecoveryParticipants(t, test.participants)
			frozen, err := service.RecoverThursday(context.Background(), closesAt, 2, time.Now().Add(time.Minute))
			if err != nil || frozen != (ThursdayRecoveryResult{Processed: 1, More: true}) {
				t.Fatalf("freeze result=%+v err=%v", frozen, err)
			}
			first, err := service.RecoverThursday(context.Background(), closesAt, 2, time.Now().Add(time.Minute))
			if err != nil || first != test.first {
				t.Fatalf("first checkpoint result=%+v want=%+v err=%v", first, test.first, err)
			}
			period := fixture.period(periodID)
			if test.second == nil {
				if period.state != PeriodStateSettled || period.settlementCursor.Valid {
					t.Fatalf("exact-limit period=%+v", period)
				}
				return
			}
			if period.state != PeriodStateSettling || !period.settlementCursor.Valid {
				t.Fatalf("checkpoint period=%+v", period)
			}
			second, err := service.RecoverThursday(context.Background(), closesAt, 2, time.Now().Add(time.Minute))
			if err != nil || second != *test.second {
				t.Fatalf("second checkpoint result=%+v want=%+v err=%v", second, *test.second, err)
			}
			period = fixture.period(periodID)
			if period.state != PeriodStateSettled || period.settlementCursor.Valid {
				t.Fatalf("terminal period=%+v", period)
			}
		})
	}
}

func TestThursdayLifecycleRecoveryDeadlineAndErrorRollback(t *testing.T) {
	t.Run("expired budget", func(t *testing.T) {
		opensAt := beijingThursday(2027, 6, 3)
		fixture := newActivityFixture(t, opensAt-1)
		service := newThursdayRecoveryService(t, fixture)
		periodID := createThursdayRecoveryPeriod(t, fixture, opensAt, "deadline")
		result, err := service.RecoverThursday(context.Background(), opensAt+86400, 1, time.Now().Add(-time.Second))
		if !errors.Is(err, context.DeadlineExceeded) || result != (ThursdayRecoveryResult{}) {
			t.Fatalf("expired result=%+v err=%v", result, err)
		}
		if fixture.period(periodID).state != PeriodStateConfigured {
			t.Fatalf("expired budget changed period: %+v", fixture.period(periodID))
		}
	})

	t.Run("settlement transaction", func(t *testing.T) {
		fixture, service, periodID, closesAt := seedThursdayRecoveryParticipants(t, 2)
		if result, err := service.RecoverThursday(context.Background(), closesAt, 2, time.Now().Add(time.Minute)); err != nil || result != (ThursdayRecoveryResult{Processed: 1, More: true}) {
			t.Fatalf("freeze result=%+v err=%v", result, err)
		}

		rows, err := fixture.store.DB().Query(`SELECT participant_ref,user_id FROM thursday_participants WHERE period_id=? ORDER BY participant_ref`, periodID)
		if err != nil {
			t.Fatal(err)
		}
		var participants []struct {
			ref    string
			userID int64
		}
		for rows.Next() {
			var participant struct {
				ref    string
				userID int64
			}
			if err := rows.Scan(&participant.ref, &participant.userID); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			participants = append(participants, participant)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if len(participants) != 2 {
			t.Fatalf("participants=%+v", participants)
		}
		if _, err := fixture.store.DB().Exec(`DELETE FROM credit_accounts WHERE kind='user' AND user_id=?`, participants[1].userID); err != nil {
			t.Fatal(err)
		}

		result, err := service.RecoverThursday(context.Background(), closesAt, 2, time.Now().Add(time.Minute))
		if !errors.Is(err, ErrNotFound) || result != (ThursdayRecoveryResult{}) {
			t.Fatalf("rollback result=%+v err=%v", result, err)
		}
		period := fixture.period(periodID)
		if period.state != PeriodStateSettling || period.settlementCursor.Valid || period.payoutTotal.Big().Sign() != 0 {
			t.Fatalf("period changed after rollback: %+v", period)
		}
		var settled, payouts int
		if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM thursday_participants WHERE period_id=? AND settled<>0`, periodID).Scan(&settled); err != nil {
			t.Fatal(err)
		}
		if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='thursday_payout'`).Scan(&payouts); err != nil {
			t.Fatal(err)
		}
		if settled != 0 || payouts != 0 {
			t.Fatalf("rollback left settled=%d payouts=%d", settled, payouts)
		}
		assertReservedRows(t, fixture.store.DB(), "3")
	})
}

func newThursdayRecoveryService(t *testing.T, fixture *activityFixture) *Service {
	t.Helper()
	service, err := NewService(ServiceConfig{Repository: fixture.repository})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func createThursdayRecoveryPeriod(t *testing.T, fixture *activityFixture, opensAt int64, label string) string {
	t.Helper()
	created, _, err := fixture.repository.PutThursdayNext(
		context.Background(), fixture.adminID,
		fixture.control(http.MethodPut, routeAdminThursdayNext, map[string]any{"recovery": label}),
		ThursdayNextMutation{
			ExpectedRevision: fixture.configRevision(), PeriodKey: time.Unix(opensAt, 0).In(beijingLocation).Format("2006-01-02"),
			OpensAt: opensAt, Entry: "0.001", PerUserLimit: 1, PumpsBP: PumpsBP{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return created.Value.ID
}

func seedThursdayRecoveryParticipants(t *testing.T, count int) (*activityFixture, *Service, string, int64) {
	t.Helper()
	opensAt := beijingThursday(2027, 6, 10)
	fixture := newActivityFixture(t, opensAt-1)
	service := newThursdayRecoveryService(t, fixture)
	periodID := createThursdayRecoveryPeriod(t, fixture, opensAt, fmt.Sprintf("batch-%d", count))
	fixture.setActivityConfig(true, false, true, 0, 0)
	fixture.clock.Store(opensAt)
	for index := 0; index < count; index++ {
		userID, _ := fixture.seedUser(fmt.Sprintf("lifecycle-%d", index), false)
		fixture.fundUser(userID, 1)
		period := fixture.period(periodID)
		if _, _, err := fixture.repository.ContributeThursday(
			context.Background(), userID,
			fixture.control(http.MethodPost, routeThursdayContributions, map[string]any{"index": index}),
			ThursdayContributionInput{PeriodID: periodID, ExpectedRevision: period.revision},
		); err != nil {
			t.Fatal(err)
		}
	}
	return fixture, service, periodID, opensAt + 86400
}
