package linklink

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func TestHostilePersistedSessionRowsFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		corrupt func(*fixture, string)
	}{
		{
			name: "board shape",
			corrupt: func(fixture *fixture, sessionID string) {
				if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET board_blob=? WHERE id=?`, []byte{1}, sessionID); err != nil {
					fixture.t.Fatal(err)
				}
			},
		},
		{
			name: "removed bitmap out of bounds",
			spec: game.LinkLinkSpec10x10,
			corrupt: func(fixture *fixture, sessionID string) {
				value := fixture.loadBoard(sessionID)
				value.removed[len(value.removed)-1] |= 1 << 7
				fixture.hostileUncheckedUpdate(`UPDATE game_linklink_sessions SET removed_bits=? WHERE id=?`, value.removed, sessionID)
			},
		},
		{
			name: "board tile domain",
			corrupt: func(fixture *fixture, sessionID string) {
				value := fixture.loadBoard(sessionID)
				value.tiles[0] = byte(value.definition.TileTypes + 1)
				if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET board_blob=? WHERE id=?`, value.tiles, sessionID); err != nil {
					fixture.t.Fatal(err)
				}
			},
		},
		{
			name: "deadline above time domain",
			corrupt: func(fixture *fixture, sessionID string) {
				fixture.hostileUpdateWithoutScalarGuard(`UPDATE game_linklink_sessions SET created_at=?,updated_at=?,deadline=? WHERE id=?`,
					int64(253402300700), int64(253402300700), int64(253402300850), sessionID)
			},
		},
		{
			name: "dead board",
			corrupt: func(fixture *fixture, sessionID string) {
				definition, _ := resolveSpec(game.LinkLinkSpec6x8)
				dead := validBoardWithActive(definition, map[Coordinate]byte{
					{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 2,
					{Row: 1, Col: 0}: 2, {Row: 1, Col: 1}: 1,
				})
				fixture.replaceBoard(sessionID, dead, definition.totalPairs()-2)
			},
		},
		{
			name: "deadline snapshot",
			corrupt: func(fixture *fixture, sessionID string) {
				if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET deadline=deadline+1 WHERE id=?`, sessionID); err != nil {
					fixture.t.Fatal(err)
				}
			},
		},
		{
			name: "state",
			corrupt: func(fixture *fixture, sessionID string) {
				fixture.hostileUncheckedUpdate(`UPDATE game_linklink_sessions SET state='timed_out' WHERE id=?`, sessionID)
			},
		},
		{
			name: "revision relation",
			corrupt: func(fixture *fixture, sessionID string) {
				two, err := db.ParseU128Decimal("2")
				if err != nil {
					fixture.t.Fatal(err)
				}
				if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET revision=? WHERE id=?`, db.EncodeU128(two), sessionID); err != nil {
					fixture.t.Fatal(err)
				}
			},
		},
		{
			name: "zero price",
			corrupt: func(fixture *fixture, sessionID string) {
				if _, err := fixture.database.Exec(`UPDATE game_linklink_sessions SET price_milli=0 WHERE id=?`, sessionID); err != nil {
					fixture.t.Fatal(err)
				}
			},
		},
		{
			name: "price above money bound",
			corrupt: func(fixture *fixture, sessionID string) {
				fixture.hostileUncheckedUpdate(`UPDATE game_linklink_sessions SET price_milli=? WHERE id=?`, game.MaxMoneyMilli+1, sessionID)
			},
		},
		{
			name: "operation identity",
			corrupt: func(fixture *fixture, sessionID string) {
				fixture.hostileUncheckedUpdate(`UPDATE game_linklink_sessions SET operation_id='op_invalid' WHERE id=?`, sessionID)
			},
		},
		{
			name: "request hash shape",
			corrupt: func(fixture *fixture, sessionID string) {
				fixture.hostileUncheckedUpdate(`UPDATE game_linklink_sessions SET request_hash=? WHERE id=?`, []byte{1}, sessionID)
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			userID, binding := fixture.seedUser("hostile-persisted-"+test.name, testFunding)
			spec := test.spec
			if spec == "" {
				spec = game.LinkLinkSpec6x8
			}
			started, err := fixture.service.Start(context.Background(), StartInput{
				UserID: userID, Spec: spec, IdempotencyKey: fixture.key(170 + index),
			})
			if err != nil {
				t.Fatal(err)
			}
			test.corrupt(fixture, started.State.SessionID)

			if current, err := fixture.service.Read(context.Background(), ReadInput{UserID: userID, SessionBinding: binding}); current.State != nil || current.Summary != nil || !errors.Is(err, ErrInvariant) {
				t.Fatalf("read accepted hostile row: current=%+v err=%v", current, err)
			}
			if err := fixture.service.RecoverBeforeListen(context.Background()); !errors.Is(err, ErrInvariant) {
				t.Fatalf("recovery accepted hostile row: %v", err)
			}
			if fixture.service.recovered.Load() {
				t.Fatal("failed recovery left the service marked recovered")
			}
			if err := fixture.service.StartWorker(context.Background()); err == nil {
				t.Fatal("worker started after hostile recovery failure")
			}
			if fixture.scalar(`SELECT COUNT(*) FROM game_linklink_sessions WHERE id=?`, started.State.SessionID) != 1 ||
				fixture.scalar(`SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=?`, started.State.SessionID) != 0 ||
				fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='linklink_entry' AND actor_user_id=?`, userID) != 1 ||
				fixture.balance(userID) != "999000" {
				t.Fatal("hostile row rejection mutated game or economic authority")
			}
		})
	}
}

func (fixture *fixture) hostileUncheckedUpdate(query string, arguments ...any) {
	fixture.t.Helper()
	if _, err := fixture.database.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
		fixture.t.Fatal(err)
	}
	defer func() {
		if _, err := fixture.database.Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
			fixture.t.Errorf("restore check constraints: %v", err)
		}
	}()
	if _, err := fixture.database.Exec(query, arguments...); err != nil {
		fixture.t.Fatal(err)
	}
}

func (fixture *fixture) hostileUpdateWithoutScalarGuard(query string, arguments ...any) {
	fixture.t.Helper()
	var triggerSQL string
	if err := fixture.database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='linklink_scalar_update_guard'`).Scan(&triggerSQL); err != nil {
		fixture.t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`DROP TRIGGER linklink_scalar_update_guard`); err != nil {
		fixture.t.Fatal(err)
	}
	defer func() {
		if _, err := fixture.database.Exec(triggerSQL); err != nil {
			fixture.t.Errorf("restore scalar guard: %v", err)
		}
	}()
	fixture.hostileUncheckedUpdate(query, arguments...)
}
