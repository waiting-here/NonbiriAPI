package companion

import (
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/program"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
)

func TestBuiltInOpponentsFinishOnLiveService(t *testing.T) {
	for _, game := range []string{"bidding", "gwent", "likes"} {
		t.Run(game, func(t *testing.T) {
			s := fixture(t)
			s.programs = program.New(s.shared)
			if _, err := s.shared.Database.Exec(db.CompanionProgramsSQL); err != nil {
				t.Fatal(err)
			}
			if _, err := s.shared.Database.Exec(`INSERT INTO companion_programs(game,name,active_version,last_version) VALUES(?,'official',1,1)`, game); err != nil {
				t.Fatal(err)
			}
			tx, err := s.shared.Database.Begin()
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.create(t.Context(), tx, 1, game, token(20), start{Mode: "standard", Program: "private", Difficulty: 1})
			tx.Rollback()
			if !errors.Is(err, runner.ErrUploadsDisabled) {
				t.Fatal("private selection admitted", err)
			}
			m := createGame(t, s, 1, game, "official", token(20))
			if !m.Official || m.Version != 0 {
				t.Fatal("match did not pin the built-in", m.binding())
			}
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
