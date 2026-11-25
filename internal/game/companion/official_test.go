package companion

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/program"
)

type builtInFixture struct {
	testPrograms
	service *program.Service
}

func (p builtInFixture) ResolveTx(_ context.Context, _ *sql.Tx, _ int64, game, name string, difficulty int) (program.Binding, error) {
	return program.Binding{Game: game, Name: name, Difficulty: difficulty, Official: true}, nil
}
func (p builtInFixture) Decide(ctx context.Context, b program.Binding, q, c Raw) (Raw, error) {
	return p.service.Decide(ctx, b, q, c)
}
func TestBuiltInOpponentsFinishOnLiveService(t *testing.T) {
	for _, game := range []string{"bidding", "gwent", "likes"} {
		t.Run(game, func(t *testing.T) {
			s := fixture(t)
			s.programs = builtInFixture{service: program.New(s.shared)}
			m := createGame(t, s, 1, game, "official", token(20))
			r, err := rulesFor(game, m.Mode)
			if err != nil {
				t.Fatal(err)
			}
			for range 500 {
				m = loadGame(t, s, m)
				if m.Status != "active" {
					break
				}
				if m.AIState == "queued" {
					if err := s.runOne(t.Context()); err != nil {
						t.Fatal(err)
					}
					continue
				}
				tx, err := s.shared.Database.Begin()
				if err != nil {
					t.Fatal(err)
				}
				w, err := r.observe(m.Frame, &m.Record, m.Seat, false)
				if err == nil {
					err = m.apply(r, m.Seat, example(t, w))
				}
				if err == nil {
					err = m.update(t.Context(), tx, time.Now())
				}
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			if m.Status != "completed" {
				t.Fatal(m.Status, m.Reason)
			}
			f, h, err := replayAt(t.Context(), m.Record, len(m.Record.Actions))
			if err != nil {
				t.Fatal(err)
			}
			if string(encode(h.Result)) != string(encode(m.result)) || string(encode(f.Bidding)) != string(encode(m.Frame.Bidding)) || string(encode(f.Gwent)) != string(encode(m.Frame.Gwent)) || string(encode(f.Likes)) != string(encode(m.Frame.Likes)) {
				t.Fatal("production match did not replay to its persisted final state")
			}
			t.Logf("%d decision windows, scores %v", len(m.Record.Actions), m.Scores)
		})
	}
}
