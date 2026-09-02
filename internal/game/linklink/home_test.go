package linklink

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestHomeSummaryTxDeadlineBoundaryOwnershipAndNoSideEffects(t *testing.T) {
	for index, offset := range []int64{-1, 0, 1} {
		t.Run(fmt.Sprintf("deadline%+d", offset), func(t *testing.T) {
			fixture := newFixture(t)
			userID, _ := fixture.seedUser(fmt.Sprintf("home-owner-%d", index), testFunding)
			foreignID, _ := fixture.seedUser(fmt.Sprintf("home-foreign-%d", index), testFunding)
			started, err := fixture.service.Start(context.Background(), StartInput{
				UserID: userID, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(701 + index),
			})
			if err != nil || started.State == nil {
				t.Fatalf("start=(%+v,%v)", started, err)
			}
			fixture.clock.Store(started.State.Deadline + offset)

			tx, err := fixture.database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			summary, err := fixture.service.HomeSummaryTx(context.Background(), tx, HomeSummaryInput{UserID: userID})
			foreign, foreignErr := fixture.service.HomeSummaryTx(context.Background(), tx, HomeSummaryInput{UserID: foreignID})
			if err != nil || foreignErr != nil {
				_ = tx.Rollback()
				t.Fatalf("read errors owner=%v foreign=%v", err, foreignErr)
			}
			if offset < 0 {
				if len(summary.Continue) != 1 || summary.Continue[0].ResourceID != started.State.SessionID || summary.Continue[0].State != "active" {
					_ = tx.Rollback()
					t.Fatalf("deadline-1 owner summary=%+v", summary)
				}
			} else if summary.Continue == nil || len(summary.Continue) != 0 {
				_ = tx.Rollback()
				t.Fatalf("expired owner summary=%#v", summary)
			}
			if foreign.Continue == nil || len(foreign.Continue) != 0 {
				_ = tx.Rollback()
				t.Fatalf("foreign summary=%#v", foreign)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=? AND state='active'`, started.State.SessionID) != 1 ||
				fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 {
				t.Fatal("home read changed active authority or created a terminal summary")
			}
		})
	}
}

func TestHomeSummaryTxRejectsInvalidInputs(t *testing.T) {
	fixture := newFixture(t)
	if _, err := fixture.service.HomeSummaryTx(context.Background(), nil, HomeSummaryInput{UserID: 1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil tx error=%v", err)
	}
	tx, err := fixture.database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := fixture.service.HomeSummaryTx(context.Background(), tx, HomeSummaryInput{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid user error=%v", err)
	}
	if err := fixture.service.Close(); err != nil {
		t.Fatalf("close service: %v", err)
	}
	if _, err := fixture.service.HomeSummaryTx(context.Background(), tx, HomeSummaryInput{UserID: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed service error=%v", err)
	}
}
