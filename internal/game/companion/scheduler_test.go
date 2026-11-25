package companion

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/program"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestFaultsTerminateAndReleaseTheGame(t *testing.T) {
	for _, test := range []struct {
		reason string
		answer func(context.Context, Raw) (Raw, error)
	}{
		{"program_error", func(context.Context, Raw) (Raw, error) { return nil, errors.New("worker stopped") }},
		{"invalid_output", func(context.Context, Raw) (Raw, error) { return Raw("{}"), nil }},
		{"invalid_action", func(ctx context.Context, q Raw) (Raw, error) {
			var a struct {
				ID string `json:"decision_id"`
			}
			if err := json.Unmarshal(q, &a); err != nil {
				return nil, err
			}
			return encode(reply{Protocol: protocol, ID: a.ID, Action: Raw("{}")}), nil
		}},
	} {
		t.Run(test.reason, func(t *testing.T) {
			s := fixture(t)
			m := createGame(t, s, 1, "gwent", "official", token(20))
			s.programs = testPrograms{run: func(c context.Context, _ program.Binding, q, _ Raw) (Raw, error) { return test.answer(c, q) }}
			if err := s.runOne(t.Context()); err != nil {
				t.Fatal(err)
			}
			m = loadGame(t, s, m)
			if m.Status != "technical" || m.Reason != test.reason || m.Ended == 0 {
				t.Fatal(m.result)
			}
			createGame(t, s, 1, "gwent", "official", token(20))
		})
	}
}
func TestMixedQueueReservationAndQueueTime(t *testing.T) {
	s := fixture(t)
	ctx := t.Context()
	gameA := createGame(t, s, 1, "gwent", "official", token(20))
	gameB := createGame(t, s, 2, "likes", "mine", token(20))
	gameC := createGame(t, s, 3, "gwent", "official", token(20))
	// Simulate a decision which has already waited longer than its execution budget.
	_, err := s.shared.Database.Exec("UPDATE companion_matches SET queued_at=queued_at-30000 WHERE id=?", gameA.ID)
	if err != nil {
		t.Fatal(err)
	}
	var executing atomic.Int32
	slots := make(chan time.Duration, 3)
	release := make(chan struct{})
	s.programs = testPrograms{run: func(ctx context.Context, _ program.Binding, _ Raw, _ Raw) (Raw, error) {
		n := executing.Add(1)
		defer executing.Add(-1)
		if n > workers {
			return nil, errors.New("extra worker")
		}
		deadline, _ := ctx.Deadline()
		slots <- time.Until(deadline)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, errors.New("completed fixture")
	}}
	s.startWorkers(ctx)
	defer s.stopWorkers()
	defer close(release)
	for range workers {
		select {
		case remaining := <-slots:
			if remaining < executionBudget-time.Second {
				t.Fatalf("queue consumed execution time: %v", remaining)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	var waiting int
	if err := s.shared.Database.QueryRow("SELECT count(*) FROM companion_matches WHERE ai_state='queued'").Scan(&waiting); err != nil || waiting != 1 {
		t.Fatal(waiting, err)
	}
	var recorded int64
	if err := s.shared.Database.QueryRow("SELECT queue_ms FROM companion_matches WHERE id=?", gameA.ID).Scan(&recorded); err != nil || recorded < 30000 {
		t.Fatal(recorded, err)
	}
	start := time.Now()
	for range 30 {
		for _, m := range []match{gameA, gameB, gameC} {
			loadGame(t, s, m)
		}
	}
	t.Logf("90 independent match reads with two occupied decision slots took %s", time.Since(start))
}
func TestFullDecisionDeadline(t *testing.T) {
	if os.Getenv("NONBIRI_TEST_REAL_DECISION_DEADLINE") != "1" {
		t.Skip("explicit wall-clock timing check")
	}
	s := fixture(t)
	m := createGame(t, s, 1, "gwent", "official", token(20))
	s.programs = testPrograms{run: func(ctx context.Context, _ program.Binding, _ Raw, _ Raw) (Raw, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	start := time.Now()
	if err := s.runOne(t.Context()); err != nil {
		t.Fatal(err)
	}
	m = loadGame(t, s, m)
	if elapsed := time.Since(start); elapsed < executionBudget || elapsed > executionBudget+2*time.Second || m.Status != "technical" || m.Reason != "timeout" {
		t.Fatal(elapsed, m.result)
	}
}
