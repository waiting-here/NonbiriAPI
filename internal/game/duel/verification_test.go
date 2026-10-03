package duel_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func (f *fixture) verifyReadOnly() error {
	f.t.Helper()
	var sequence int
	var name, path string
	if err := f.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		f.t.Fatal(err)
	}
	read, err := sql.Open("sqlite", path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		f.t.Fatal(err)
	}
	defer read.Close()
	read.SetMaxOpenConns(1)
	err = duel.VerifyPersistedState(f.ctx, read, f.options.Descriptor, f.rules)
	var changes int
	if check := read.QueryRow("SELECT total_changes()").Scan(&changes); check != nil || changes != 0 {
		f.t.Fatal("verification wrote data", changes, check)
	}
	return err
}

func TestVerifyPopulatedDuelReadOnly(t *testing.T) {
	for _, kind := range []string{"bidding", "likes"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, kind)
			f.enqueue(0, 0)
			if err := f.verifyReadOnly(); err != nil {
				t.Fatal("queued", err)
			}
			f.enqueue(1, 1)
			f.tick()
			state := *f.read(0).Current
			if err := f.verifyReadOnly(); err != nil {
				t.Fatal("active", err)
			}
			if kind == "bidding" {
				f.clock.Store(*state.Deadline)
				f.tick()
				state = *f.read(0).Current
				f.action(0, state, `{"kind":"bid","card":1}`)
				f.action(1, state, `{"kind":"bid","card":2}`)
			} else {
				f.action(0, state, basicPlan)
				f.action(1, state, basicPlan)
			}
			state = *f.read(0).Current
			if err := f.verifyReadOnly(); err != nil {
				t.Fatal("persisted round", err)
			}
			if _, err := f.s.Surrender(f.ctx, duel.ActionInput{Identity: f.identity(0), IdempotencyKey: f.key(), SessionID: state.ID, PhaseSeq: state.PhaseSeq}); err != nil {
				t.Fatal(err)
			}
			if err := f.verifyReadOnly(); err != nil {
				t.Fatal("terminal", err)
			}
			var terminal int
			if err := f.db.QueryRow("SELECT count(*) FROM game_duel_sessions WHERE state='terminal'").Scan(&terminal); err != nil || terminal != 1 {
				t.Fatal(terminal, err)
			}
			f.clock.Add(duel.RetentionSeconds)
			if _, err := f.s.Retain(f.ctx, f.clock.Load(), 100, time.Now().Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			var archives, rounds int
			if err := f.db.QueryRow("SELECT (SELECT count(*) FROM game_duel_anonymous),(SELECT count(*) FROM game_duel_anonymous_rounds)").Scan(&archives, &rounds); err != nil || archives != 1 || rounds != 1 {
				t.Fatal("populated archive", archives, rounds, err)
			}
			if err := f.verifyReadOnly(); err != nil {
				t.Fatal("archive", err)
			}
			// Simulate isolated archive corruption outside the immutable write API.
			if _, err := f.db.Exec("DROP TRIGGER game_duel_anonymous_round_immutable"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE game_duel_anonymous_rounds SET record_json=json_set(record_json,'$.round',2)`); err != nil {
				t.Fatal(err)
			}
			if err := f.verifyReadOnly(); !errors.Is(err, duel.ErrInvariant) {
				t.Fatal("archive corruption accepted", err)
			}
		})
	}
}
