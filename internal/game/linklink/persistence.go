package linklink

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type sessionRecord struct {
	ID           string
	UserID       int64
	Spec         string
	State        string
	Revision     db.U128
	PriceMilli   int64
	Board        board
	PairsRemoved int
	Deadline     int64
	OperationID  string
	RequestHash  [32]byte
	CreatedAt    int64
	UpdatedAt    int64
}

func scanSession(scanner interface{ Scan(...any) error }) (sessionRecord, error) {
	var record sessionRecord
	var revisionRaw, boardBlob, removedBits, requestHash []byte
	err := scanner.Scan(
		&record.ID, &record.UserID, &record.Spec, &record.State, &revisionRaw, &record.PriceMilli,
		&boardBlob, &removedBits, &record.PairsRemoved, &record.Deadline,
		&record.OperationID, &requestHash, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return sessionRecord{}, err
	}
	record.Revision, err = db.DecodeU128(revisionRaw)
	if err != nil || record.Revision.Big().Sign() == 0 || len(requestHash) != len(record.RequestHash) {
		return sessionRecord{}, ErrInvariant
	}
	copy(record.RequestHash[:], requestHash)
	record.Board, err = decodeBoard(record.Spec, boardBlob, removedBits)
	if err != nil || !db.ValidateOpaqueID(record.ID, "ll_") || !db.ValidateOpaqueID(record.OperationID, "op_") ||
		record.UserID <= 0 || record.State != "active" || record.PriceMilli <= 0 || record.PriceMilli > game.MaxMoneyMilli {
		return sessionRecord{}, ErrInvariant
	}
	definition := record.Board.definition
	activeCount := record.Board.activeCount()
	expectedRevision := big.NewInt(int64(record.PairsRemoved) + 1)
	if record.PairsRemoved < 0 || record.PairsRemoved >= definition.totalPairs() || definition.cells()-activeCount != record.PairsRemoved*2 ||
		activeCount == 0 || !record.Board.hasMove() || record.Revision.Big().Cmp(expectedRevision) != 0 ||
		record.CreatedAt < 0 || record.CreatedAt > 253402300799 || record.Deadline < 0 || record.Deadline > 253402300799 ||
		record.Deadline != record.CreatedAt+definition.Seconds || record.UpdatedAt < record.CreatedAt || record.UpdatedAt > record.Deadline {
		return sessionRecord{}, ErrInvariant
	}
	return record, nil
}

const sessionColumns = `id,user_id,spec,state,revision,price_milli,board_blob,removed_bits,pairs_removed,deadline,operation_id,request_hash,created_at,updated_at`

func loadSessionByUser(ctx context.Context, tx *sql.Tx, userID int64) (sessionRecord, bool, error) {
	record, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_linklink_sessions WHERE user_id=?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, false, nil
	}
	if err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	return record, true, nil
}

func loadSessionByID(ctx context.Context, tx *sql.Tx, userID int64, sessionID string) (sessionRecord, bool, error) {
	record, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM game_linklink_sessions WHERE id=? AND user_id=?`, sessionID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return sessionRecord{}, false, nil
	}
	if err != nil {
		return sessionRecord{}, false, classifyDB(err)
	}
	return record, true, nil
}

func stateFromRecord(record sessionRecord, now int64) State {
	return State{
		SessionID: record.ID, Spec: record.Spec, Price: game.FormatAmount(record.PriceMilli), State: record.State,
		Revision: record.Revision.Decimal(), Board: record.Board.view(), PairsRemoved: record.PairsRemoved,
		TotalPairs: record.Board.definition.totalPairs(), StartedAt: record.CreatedAt, Deadline: record.Deadline, ServerNow: now,
	}
}

func nextRevision(revision db.U128) (db.U128, error) {
	next := new(big.Int).Add(revision.Big(), big.NewInt(1))
	value, err := db.U128FromBig(next)
	if err != nil || value.Big().Sign() == 0 {
		return db.U128{}, ErrInvariant
	}
	return value, nil
}

func loadSummary(ctx context.Context, tx *sql.Tx, userID int64, sessionID string) (Summary, bool, error) {
	var summary Summary
	var price int64
	var score sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT session_id,spec,price_milli,terminal_reason,started_at,deadline,terminal_at,pairs_removed,score
FROM game_linklink_summaries WHERE session_id=? AND user_id=?`, sessionID, userID).Scan(
		&summary.SessionID, &summary.Spec, &price, &summary.TerminalReason,
		&summary.StartedAt, &summary.Deadline, &summary.TerminalAt, &summary.PairsRemoved, &score,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, false, nil
	}
	if err != nil {
		return Summary{}, false, classifyDB(err)
	}
	definition, ok := resolveSpec(summary.Spec)
	if !ok || !db.ValidateOpaqueID(summary.SessionID, "ll_") || price <= 0 || price > game.MaxMoneyMilli || summary.PairsRemoved < 0 || summary.PairsRemoved > definition.totalPairs() ||
		summary.StartedAt < 0 || summary.StartedAt > 253402300799 || summary.Deadline < 0 || summary.Deadline > 253402300799 ||
		summary.Deadline != summary.StartedAt+definition.Seconds || summary.TerminalAt < summary.StartedAt || summary.TerminalAt > 253402300799 {
		return Summary{}, false, ErrInvariant
	}
	summary.Price = game.FormatAmount(price)
	summary.TotalPairs = definition.totalPairs()
	expectedScore := int64(summary.PairsRemoved) * 100
	if remaining := summary.Deadline - summary.TerminalAt; remaining > 0 {
		expectedScore += remaining
	}
	switch summary.TerminalReason {
	case TerminalCompleted, TerminalTimedOut:
		if !score.Valid || score.Int64 != expectedScore ||
			summary.TerminalReason == TerminalCompleted && (summary.PairsRemoved != definition.totalPairs() || summary.TerminalAt > summary.Deadline) ||
			summary.TerminalReason == TerminalTimedOut && summary.TerminalAt < summary.Deadline {
			return Summary{}, false, ErrInvariant
		}
		value := new(big.Int).SetInt64(score.Int64).String()
		summary.Score = &value
	case TerminalAbandoned:
		if score.Valid || summary.TerminalAt >= summary.Deadline {
			return Summary{}, false, ErrInvariant
		}
	default:
		return Summary{}, false, ErrInvariant
	}
	return summary, true, nil
}

func loadLatestSummary(ctx context.Context, tx *sql.Tx, userID, now int64) (Summary, bool, error) {
	cutoff := now - int64(summaryWindow/time.Second)
	var sessionID string
	err := tx.QueryRowContext(ctx, `
SELECT session_id
FROM game_linklink_summaries
WHERE user_id=? AND terminal_at>?
ORDER BY terminal_at DESC,session_id DESC
LIMIT 1`, userID, cutoff).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, false, nil
	}
	if err != nil {
		return Summary{}, false, classifyDB(err)
	}
	return loadSummary(ctx, tx, userID, sessionID)
}

func terminalScore(record sessionRecord, terminalAt int64) int64 {
	remaining := record.Deadline - terminalAt
	if remaining < 0 {
		remaining = 0
	}
	return int64(record.PairsRemoved)*100 + remaining
}

func deleteUserIdempotency(ctx context.Context, tx *sql.Tx, userID int64) error {
	actor, err := actorHash(userID)
	if err != nil {
		return ErrInvariant
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM idempotency_records WHERE scope=? AND actor_scope_hash=?`, idempotency.ScopeGameLinkLink, actor[:])
	return classifyDB(err)
}

func terminalize(ctx context.Context, tx *sql.Tx, record sessionRecord, reason string, terminalAt int64) (Summary, error) {
	if terminalAt < record.CreatedAt || terminalAt > 253402300799 {
		return Summary{}, ErrInvariant
	}
	var score any
	var scoreWire *string
	switch reason {
	case TerminalCompleted:
		if record.PairsRemoved != record.Board.definition.totalPairs() || record.Board.activeCount() != 0 || terminalAt > record.Deadline {
			return Summary{}, ErrInvariant
		}
		value := terminalScore(record, terminalAt)
		score = value
		wire := new(big.Int).SetInt64(value).String()
		scoreWire = &wire
	case TerminalTimedOut:
		if terminalAt < record.Deadline {
			return Summary{}, ErrInvariant
		}
		value := terminalScore(record, terminalAt)
		score = value
		wire := new(big.Int).SetInt64(value).String()
		scoreWire = &wire
	case TerminalAbandoned:
		score = nil
	default:
		return Summary{}, ErrInvariant
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO game_linklink_summaries(session_id,user_id,spec,price_milli,terminal_reason,started_at,deadline,terminal_at,pairs_removed,score)
VALUES(?,?,?,?,?,?,?,?,?,?)`, record.ID, record.UserID, record.Spec, record.PriceMilli, reason, record.CreatedAt, record.Deadline, terminalAt, record.PairsRemoved, score); err != nil {
		return Summary{}, classifyDB(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM game_online_leases WHERE session_id=?`, record.ID); err != nil {
		return Summary{}, classifyDB(err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM game_linklink_sessions WHERE id=? AND user_id=? AND revision=?`, record.ID, record.UserID, db.EncodeU128(record.Revision))
	if err != nil {
		return Summary{}, classifyDB(err)
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr != nil || changed != 1 {
		return Summary{}, ErrConflict
	}
	return Summary{
		SessionID: record.ID, Spec: record.Spec, Price: game.FormatAmount(record.PriceMilli), TerminalReason: reason,
		StartedAt: record.CreatedAt, Deadline: record.Deadline, TerminalAt: terminalAt,
		PairsRemoved: record.PairsRemoved, TotalPairs: record.Board.definition.totalPairs(), Score: scoreWire,
	}, nil
}

func marshalResult(result Result) ([]byte, error) {
	if !result.valid() {
		return nil, ErrInvariant
	}
	if result.State != nil {
		return json.Marshal(struct {
			*State
			MatchPath []Coordinate `json:"match_path,omitempty"`
		}{result.State, result.MatchPath})
	}
	return json.Marshal(struct {
		*Summary
		MatchPath []Coordinate `json:"match_path,omitempty"`
	}{result.Summary, result.MatchPath})
}

func replayResult(decision idempotency.Decision) (Result, error) {
	if decision.Kind != idempotency.Replay || decision.HTTPStatus < 200 || decision.HTTPStatus > 299 {
		return Result{}, ErrInvariant
	}
	var probe struct {
		TerminalReason string       `json:"terminal_reason"`
		MatchPath      []Coordinate `json:"match_path"`
	}
	if json.Unmarshal(decision.ResponseBody, &probe) != nil {
		return Result{}, ErrInvariant
	}
	if probe.TerminalReason != "" {
		var summary Summary
		if json.Unmarshal(decision.ResponseBody, &summary) != nil || summary.SessionID == "" {
			return Result{}, ErrInvariant
		}
		result := summaryResult(summary, true)
		result.MatchPath = probe.MatchPath
		return result, nil
	}
	var state State
	if json.Unmarshal(decision.ResponseBody, &state) != nil || state.SessionID == "" {
		return Result{}, ErrInvariant
	}
	result := stateResult(state, decision.HTTPStatus, true)
	result.MatchPath = probe.MatchPath
	return result, nil
}

// existingReplay checks only an unexpired, already accepted identity. It is
// used by a late action so the same transaction can still materialize the
// absolute deadline while preserving an earlier successful response exactly.
// A missing or expired record is not reserved: late new intents must remain
// write-free apart from the authoritative timeout transition.
func existingReplay(ctx context.Context, tx *sql.Tx, actorHash [32]byte, key string, requestHash [32]byte, now int64) (Result, bool, error) {
	keyHash, err := idempotency.KeyHash(key)
	if err != nil {
		return Result{}, false, ErrInvalidRequest
	}
	var storedHash, body []byte
	var state string
	var status int
	var expiresAt int64
	err = tx.QueryRowContext(ctx, `
SELECT request_hash,state,http_status,response_body,expires_at
FROM idempotency_records
WHERE scope=? AND actor_scope_hash=? AND key_hash=?`,
		idempotency.ScopeGameLinkLink, actorHash[:], keyHash[:]).Scan(&storedHash, &state, &status, &body, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) || err == nil && expiresAt <= now {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, classifyDB(err)
	}
	if len(storedHash) != len(requestHash) || !bytes.Equal(storedHash, requestHash[:]) {
		return Result{}, false, ErrConflict
	}
	if state == "accepted" {
		return Result{}, false, ErrConflict
	}
	if state != "completed" {
		return Result{}, false, ErrInvariant
	}
	result, err := replayResult(idempotency.Decision{Kind: idempotency.Replay, HTTPStatus: status, ResponseBody: body})
	if err != nil {
		return Result{}, false, err
	}
	return result, true, nil
}

func completeResult(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, result Result) error {
	body, err := marshalResult(result)
	if err != nil {
		return err
	}
	if err := idempotency.Complete(ctx, tx, decision, result.HTTPStatus, body); err != nil {
		return mapIdempotency(err)
	}
	return nil
}

func lateActionExists(ctx context.Context, tx *sql.Tx, userID int64, sessionID string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM game_linklink_summaries WHERE session_id=? AND user_id=?`, sessionID, userID).Scan(&count); err != nil {
		return false, classifyDB(err)
	}
	return count == 1, nil
}
