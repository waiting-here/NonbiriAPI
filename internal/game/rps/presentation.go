package rps

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

type sessionPresentation struct {
	PoolTieCount  *db.U128
	QuickGestures [3]*string
}

func validPresentationGestures(gestures [3]*string, mode, reason string) bool {
	known := 0
	for _, gesture := range gestures {
		if gesture != nil {
			if !validGesture(*gesture) {
				return false
			}
			known++
		}
	}
	return known == 0 || known == 3 && mode == game.RPSModeQuick && reason == TerminalQuickResolved
}

func readPresentationGestures(raw [3]sql.NullString, mode, reason string) ([3]*string, error) {
	var gestures [3]*string
	for index, value := range raw {
		if value.Valid {
			gesture := value.String
			gestures[index] = &gesture
		}
	}
	if !validPresentationGestures(gestures, mode, reason) {
		return [3]*string{}, ErrInvariant
	}
	return gestures, nil
}

func loadSessionPresentation(ctx context.Context, tx *sql.Tx, record *sessionRecord) error {
	var count []byte
	var gestures [3]sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT pool_tie_count,quick_seat0_gesture,quick_seat1_gesture,quick_seat2_gesture
FROM game_rps_presentation WHERE session_id=?`, record.ID).Scan(&count, &gestures[0], &gestures[1], &gestures[2])
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return classifyDB(err)
	}
	if count != nil {
		value, err := db.DecodeU128(count)
		if err != nil {
			return ErrInvariant
		}
		record.Presentation.PoolTieCount = &value
	}
	reason := ""
	if record.TerminalReason != nil {
		reason = *record.TerminalReason
	}
	record.Presentation.QuickGestures, err = readPresentationGestures(gestures, record.Mode, reason)
	return err
}

func persistSessionPresentation(ctx context.Context, tx *sql.Tx, record *sessionRecord) error {
	value := record.Presentation
	_, err := tx.ExecContext(ctx, `INSERT INTO game_rps_presentation
(session_id,pool_tie_count,quick_seat0_gesture,quick_seat1_gesture,quick_seat2_gesture) VALUES(?,?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET pool_tie_count=excluded.pool_tie_count,
quick_seat0_gesture=excluded.quick_seat0_gesture,quick_seat1_gesture=excluded.quick_seat1_gesture,
quick_seat2_gesture=excluded.quick_seat2_gesture`, record.ID, nullableU128(value.PoolTieCount),
		nullableString(value.QuickGestures[0]), nullableString(value.QuickGestures[1]), nullableString(value.QuickGestures[2]))
	return classifyDB(err)
}

// Transfers describe the player's wallet at entry and exit, independently of
// the amounts cycled through multiple rounds inside the match.
func presentationTransfers(buyInRaw, cashOutRaw []byte, sign int, magnitude db.U128) (*string, *string, error) {
	if buyInRaw == nil && cashOutRaw == nil {
		return nil, nil, nil
	}
	buyIn, err := db.DecodeU128(buyInRaw)
	if err != nil {
		return nil, nil, ErrInvariant
	}
	cashOut, err := db.DecodeU128(cashOutRaw)
	if err != nil {
		return nil, nil, ErrInvariant
	}
	actualSign, actualMagnitude, err := walletNet(cashOut.Big(), buyIn.Big())
	if err != nil || actualSign != sign || actualMagnitude.Big().Cmp(magnitude.Big()) != 0 {
		return nil, nil, ErrInvariant
	}
	return stringAmount(&buyIn), stringAmount(&cashOut), nil
}
