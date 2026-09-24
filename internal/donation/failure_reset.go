package donation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeOwnerFailureReset   = "/api/donations/{id}/keys/{keyId}/failure-streak-reset"
	routeAdminFailureReset   = "/admin/api/donation-keys/failure-streak-reset"
	routeStewardFailureReset = "/api/steward/donation-keys/failure-streak-reset"
	maxFailureResetItems     = 100
	streakResetAssignments   = "streak_generation=?,failure_streak=?,next_claim_seq=?,next_fold_seq=?,failure_disabled=0"
)

type FailureResetRef struct {
	DonationID       string `json:"donation_id"`
	KeyID            string `json:"key_id"`
	ExpectedRevision string `json:"expected_revision"`
}
type FailureResetReceipt struct {
	DonationID    string `json:"donation_id"`
	KeyID         string `json:"key_id"`
	Revision      string `json:"revision"`
	FailureStreak string `json:"failure_streak"`
}
type FailureResetResult struct {
	DonationID string  `json:"donation_id"`
	KeyID      string  `json:"key_id"`
	Status     string  `json:"status"`
	Revision   *string `json:"revision"`
}
type FailureResetBatch struct {
	Results []FailureResetResult `json:"results"`
	Counts  struct {
		Processed string `json:"processed"`
		Reset     string `json:"reset"`
		Skipped   string `json:"skipped"`
	} `json:"counts"`
}
type failureResetItem struct{ donationID, keyID, expected int64 }

func failureResetRef(donationID, keyID, revision int64) FailureResetRef {
	return FailureResetRef{strconv.FormatInt(donationID, 10), strconv.FormatInt(keyID, 10), strconv.FormatInt(revision, 10)}
}

// Both the existing key editor and the reset-only commands start the same
// fresh generation. The counters belong to that generation, not to the key's usage.
func streakResetValues(blob []byte) ([]any, error) {
	generation, err := db.DecodeU128(blob)
	if err != nil {
		return nil, ErrInvariant
	}
	generation, err = incrementU128(generation)
	if err != nil {
		return nil, err
	}
	return []any{db.EncodeU128(generation), db.EncodeU128(db.U128{}),
		db.EncodeU128(db.U128{15: 1}), db.EncodeU128(db.U128{15: 1})}, nil
}

type resetKeyState struct {
	generation []byte
	eligible   bool
}

// A disabled key remains eligible for a counter reset. Membership, approval
// and expiry still control its ability to serve requests.
func readResetKeyTx(ctx context.Context, tx *sql.Tx, donationID, keyID, now int64) (resetKeyState, error) {
	var state resetKeyState
	err := tx.QueryRowContext(ctx, `SELECT dk.streak_generation,
 dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?)
 AND EXISTS(SELECT 1 FROM donation_key_memberships m
 JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
 WHERE m.donation_key_id=dk.id AND m.donation_id=dk.donation_id
 AND m.endpoint_key_id=dk.endpoint_key_id AND e.user_id=d.user_id)
 FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id
 WHERE dk.id=? AND dk.donation_id=?`, now, keyID, donationID).Scan(&state.generation, &state.eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return state, ErrNotFound
	}
	return state, err
}

func resetDonationKeyTx(ctx context.Context, tx *sql.Tx, item failureResetItem, state resetKeyState, actorID int64, role string, now int64) error {
	if item.expected == math.MaxInt64 {
		return ErrInvariant
	}
	values, err := streakResetValues(state.generation)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET `+streakResetAssignments+`,updated_at=? WHERE id=? AND donation_id=?`,
		append(values, now, item.keyID, item.donationID)...)
	if err != nil {
		return fmt.Errorf("donation: reset failure generation: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE donations SET revision=revision+1,updated_at=?
 WHERE id=? AND status='approved' AND revision=?`, now, item.donationID, item.expected)
	if err != nil {
		return fmt.Errorf("donation: advance reset revision: %w", err)
	}
	if err := requireOne(result); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(
 donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
 VALUES(?,?,?,?,'failure_streak_reset','',?)`, item.donationID, item.expected+1, actorID, role, now)
	return err
}

func (s *Service) resetFailureOwner(ctx context.Context, userID, donationID, keyID, expected int64, mutation resources.ControlMutation) (resources.MutationResult[FailureResetReceipt], error) {
	var empty resources.MutationResult[FailureResetReceipt]
	if ctx == nil || expected <= 0 || !validMutation(mutation, http.MethodPost, routeOwnerFailureReset, donationID, keyID) {
		return empty, ErrInvalidRequest
	}
	tx, err := s.beginOwnerTx(ctx, userID)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	var status string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT status,revision FROM donations WHERE id=? AND user_id=?`, donationID, userID).Scan(&status, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	state, err := readResetKeyTx(ctx, tx, donationID, keyID, now)
	if err != nil {
		return empty, err
	}
	if status != "approved" || !state.eligible {
		return empty, ErrConflict
	}
	decision, err := beginMutation(ctx, tx, "user", userID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[FailureResetReceipt](decision)
	}
	if revision != expected {
		return empty, ErrConflict
	}
	if err := resetDonationKeyTx(ctx, tx, failureResetItem{donationID, keyID, expected}, state, userID, "", now); err != nil {
		return empty, err
	}
	ref := failureResetRef(donationID, keyID, revision+1)
	value := FailureResetReceipt{ref.DonationID, ref.KeyID, ref.ExpectedRevision, "0"}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

func (s *Service) resetFailureBatch(ctx context.Context, role reviewerRole, userID int64, items []failureResetItem, mutation resources.ControlMutation) (resources.MutationResult[FailureResetBatch], error) {
	var empty resources.MutationResult[FailureResetBatch]
	route := routeAdminFailureReset
	if role == reviewerSteward {
		route = routeStewardFailureReset
	}
	if ctx == nil || len(items) == 0 || len(items) > maxFailureResetItems || !validScopedMutation(ctx, mutation, http.MethodPost, route) {
		return empty, ErrInvalidRequest
	}
	tx, scope, err := s.beginScopedTx(ctx, role, userID, false)
	if err != nil {
		return empty, err
	}
	committed := false
	actorID, auditRole := scope.ActorID, scope.AuditRole(role == reviewerAdmin)
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	allowed := make([]bool, len(items))
	for index, item := range items {
		if !scope.Trainee {
			allowed[index] = true
			continue
		}
		err := scope.RequireKey(ctx, tx, item.donationID, item.keyID, now, false)
		if err != nil && !errors.Is(scopeError(err), ErrNotFound) {
			return empty, scopeError(err)
		}
		allowed[index] = err == nil
	}
	decision, err := beginMutation(ctx, tx, auditRole, actorID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		if scope.Trainee {
			for _, value := range allowed {
				if !value {
					return empty, ErrNotFound
				}
			}
		}
		return replay[FailureResetBatch](decision)
	}
	// The request boundary has already checked positive IDs, duplicate keys and
	// one expected revision per donation. Preserve request order in the receipt.
	groups := make(map[int64][]int)
	order := []int64{}
	value := FailureResetBatch{Results: make([]FailureResetResult, len(items))}
	for index, item := range items {
		if _, found := groups[item.donationID]; !found {
			order = append(order, item.donationID)
		}
		groups[item.donationID] = append(groups[item.donationID], index)
		ref := failureResetRef(item.donationID, item.keyID, item.expected)
		value.Results[index] = FailureResetResult{DonationID: ref.DonationID, KeyID: ref.KeyID}
	}
	reset := 0
	for _, donationID := range order {
		indices := groups[donationID]
		var status string
		var revision int64
		var ordinary bool
		err := tx.QueryRowContext(ctx, `SELECT status,revision,status IN ('pending','approved') OR terminal_at>?
   FROM donations WHERE id=?`, now-terminalRetention, donationID).Scan(&status, &revision, &ordinary)
		groupStatus := ""
		if errors.Is(err, sql.ErrNoRows) || err == nil && role == reviewerSteward && !ordinary {
			groupStatus = "not_found"
		} else if err != nil {
			return empty, err
		} else if status != "approved" {
			groupStatus = "ineligible"
		} else if revision != items[indices[0]].expected {
			groupStatus = "conflict"
		}
		for _, index := range indices {
			result := &value.Results[index]
			if !allowed[index] {
				result.Status = "not_found"
				continue
			}
			result.Status = groupStatus
			if groupStatus != "" {
				continue
			}
			item := items[index]
			state, err := readResetKeyTx(ctx, tx, donationID, item.keyID, now)
			if errors.Is(err, ErrNotFound) {
				result.Status = "not_found"
				continue
			}
			if err != nil {
				return empty, err
			}
			if !state.eligible {
				result.Status = "ineligible"
				continue
			}
			item.expected = revision
			if err := resetDonationKeyTx(ctx, tx, item, state, actorID, auditRole, now); err != nil {
				return empty, err
			}
			revision++
			reset++
			result.Status = "reset"
		}
		if groupStatus != "not_found" {
			latest := strconv.FormatInt(revision, 10)
			for _, index := range indices {
				if !scope.Trainee || allowed[index] && value.Results[index].Status != "not_found" {
					value.Results[index].Revision = &latest
				}
			}
		}
	}
	value.Counts.Processed = strconv.Itoa(len(items))
	value.Counts.Reset = strconv.Itoa(reset)
	value.Counts.Skipped = strconv.Itoa(len(items) - reset)
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}
