package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrKeyRateLimited means no attempt was admitted. Callers may try another
// binding without treating this as an upstream failure.
var ErrKeyRateLimited = errors.New("claim: endpoint key rate limited")

// Pending claims reserve both limits. Dispatch transfers the RPM slot into
// the rolling window; completion frees concurrency and keeps the dispatch time.
func readKeyLimitsTx(ctx context.Context, tx *sql.Tx, keyID, at int64) (keyLimitState, error) {
	var state keyLimitState
	var concurrencyMode, rpmMode string
	err := tx.QueryRowContext(ctx, `SELECT max_concurrency,max_rpm,concurrency_overflow,rpm_overflow,
concurrency_queue_limit,rpm_queue_limit FROM endpoint_key_limits WHERE endpoint_key_id=?`, keyID).
		Scan(&state.concurrency, &state.rpm, &concurrencyMode, &rpmMode, &state.concurrencyCap, &state.rpmCap)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("claim: read endpoint key limits: %w", err)
	}
	state.concurrencyQueue, state.rpmQueue = concurrencyMode == "queue", rpmMode == "queue"
	if state.concurrency == 0 && state.rpm == 0 {
		return state, nil
	}
	var active, pending int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(state='claimed'),0)
FROM dispatch_claims WHERE endpoint_key_id=? AND purpose<>'discovery' AND state IN ('claimed','dispatched')`, keyID).Scan(&active, &pending); err != nil {
		return state, fmt.Errorf("claim: count endpoint key reservations: %w", err)
	}
	if state.concurrency > 0 && active >= state.concurrency {
		state.blocked |= keyConcurrency
	}
	if state.rpm > 0 {
		var recent int64
		var oldest sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),MIN(dispatched_at) FROM dispatch_claims
WHERE endpoint_key_id=? AND purpose<>'discovery' AND dispatched_at IS NOT NULL AND dispatched_at>?`, keyID, at-60).Scan(&recent, &oldest); err != nil {
			return state, fmt.Errorf("claim: count endpoint key dispatches: %w", err)
		}
		if pending >= state.rpm || recent >= state.rpm-pending {
			state.blocked |= keyRPM
		}
		if oldest.Valid {
			state.nextRPM = oldest.Int64 + 60
		}
	}
	return state, nil
}

type keyLimitReason uint8

const (
	keyConcurrency keyLimitReason = 1 << iota
	keyRPM
)

type keyLimitState struct {
	concurrency, rpm           int64
	concurrencyQueue, rpmQueue bool
	concurrencyCap, rpmCap     int64
	blocked                    keyLimitReason
	nextRPM                    int64
}

func (state keyLimitState) enabled(reason keyLimitReason) keyLimitReason {
	if state.concurrency == 0 {
		reason &^= keyConcurrency
	}
	if state.rpm == 0 {
		reason &^= keyRPM
	}
	return reason
}

func (state keyLimitState) queueAllowed(reason keyLimitReason) bool {
	return (reason&keyConcurrency == 0 || state.concurrencyQueue) && (reason&keyRPM == 0 || state.rpmQueue)
}

func (state keyLimitState) queueFits(reason keyLimitReason, waiting int) bool {
	return (reason&keyConcurrency == 0 || state.concurrencyCap == 0 || int64(waiting) < state.concurrencyCap) &&
		(reason&keyRPM == 0 || state.rpmCap == 0 || int64(waiting) < state.rpmCap)
}

// Only this error carries queue eligibility. Health and backoff limits keep
// their ordinary sentinel and must never create a key waiter.
type keyQueueError struct{ candidates []ClaimInput }

func (*keyQueueError) Error() string { return ErrKeyRateLimited.Error() }
func (*keyQueueError) Unwrap() error { return ErrKeyRateLimited }

// KeyQueueCandidates returns the explicitly queueable endpoint key candidates
// in the order established by the claim attempt.
func KeyQueueCandidates(err error) []ClaimInput {
	var blocked *keyQueueError
	if !errors.As(err, &blocked) {
		return nil
	}
	return append([]ClaimInput(nil), blocked.candidates...)
}

func (s *Service) checkKeyLimitsTx(ctx context.Context, tx *sql.Tx, input ClaimInput, at int64) error {
	if input.Purpose == PurposeDiscovery {
		return nil
	}
	state, err := readKeyLimitsTx(ctx, tx, input.Candidate.EndpointKeyID, at)
	if err != nil {
		return err
	}
	q := &s.keyQueue
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrDependencyUnavailable
	}
	entry := q.keys[input.Candidate.EndpointKeyID]
	reason := state.blocked
	if entry != nil && (entry.handoff != nil || entry.waiters.Len() > 0) {
		if input.queueTicket != nil && entry.handoff == nil && entry.front() == input.queueTicket {
			if reason == 0 {
				entry.handoff = input.queueTicket
				return nil
			}
		} else {
			reason |= state.enabled(entry.reason())
			if state.queueAllowed(reason) {
				return &keyQueueError{candidates: []ClaimInput{input}}
			}
			return ErrKeyRateLimited
		}
	} else if input.queueTicket != nil {
		return ErrConflict
	}
	if reason == 0 {
		return nil
	}
	if state.queueAllowed(reason) {
		return &keyQueueError{candidates: []ClaimInput{input}}
	}
	return ErrKeyRateLimited
}
