package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

// A pending receipt contributes one unit of provisional load. The same
// transaction that chooses a key inserts that receipt, so simultaneous first
// claims cannot all observe an empty window.
func (s *Service) claimBalancedTx(ctx context.Context, tx *sql.Tx, claimID string, at int64, input ClaimInput) (Handle, error) {
	var modelID, revision int64
	err := tx.QueryRowContext(ctx, `SELECT r.charity_model_id,s.revision
FROM charity_reservations r
JOIN charity_routing_settings s ON s.model_id=r.charity_model_id
JOIN charity_model_routing m ON m.model_id=r.charity_model_id
WHERE r.logical_request_id=? AND r.user_id=? AND m.strategy='cache_balanced'`,
		input.RequestID, input.ActorUserID).Scan(&modelID, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Handle{}, ErrModelUnavailable
	}
	if err != nil {
		return Handle{}, fmt.Errorf("claim: read balanced routing settings: %w", err)
	}
	return s.claimBalancedCandidatesTx(ctx, tx, claimID, at, input, modelID, revision, false)
}

func (s *Service) claimBalancedCandidatesTx(ctx context.Context, tx *sql.Tx, claimID string, at int64, input ClaimInput, modelID, revision int64, personal bool) (Handle, error) {
	type scored struct {
		candidate BalancedCandidate
		physical  [32]byte
		load      int64
		preferred bool
		order     int
	}
	choices := make([]scored, 0, len(input.BalancedCandidates))
	affinity, err := currentAffinityTx(ctx, tx, input.ActorUserID, modelID, personal)
	if err != nil {
		return Handle{}, err
	}
	for index, option := range input.BalancedCandidates {
		var secretRefID int64
		var baseURL string
		err := tx.QueryRowContext(ctx, `SELECT k.secret_ref_id,s.canonical_base_url
FROM endpoint_keys k JOIN endpoint_key_secrets s ON s.id=k.secret_ref_id
WHERE k.id=?`, option.Candidate.EndpointKeyID).Scan(&secretRefID, &baseURL)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Handle{}, fmt.Errorf("claim: read candidate physical identity: %w", err)
		}
		if baseURL != option.Candidate.CanonicalBaseURL {
			continue
		}
		physical, err := physicalCharityKeyID(option.Candidate.EndpointKeyID, secretRefID, baseURL)
		if err != nil {
			return Handle{}, err
		}
		load, err := physicalDispatchLoadTx(ctx, tx, physical, at)
		if err != nil {
			return Handle{}, err
		}
		choices = append(choices, scored{
			candidate: option, physical: physical, load: load, order: index,
			preferred: affinity != nil && affinity.revision == revision && affinity.expiresAt > at &&
				affinity.endpointKeyID == option.Candidate.EndpointKeyID && string(affinity.physicalID) == string(physical[:]),
		})
	}
	if len(choices) == 0 {
		return Handle{}, ErrNotFound
	}
	// Stable sorting preserves the snapshot's random permutation within each
	// equal-load group. Charity snapshots additionally apply expiry weights.
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].preferred != choices[j].preferred {
			return choices[i].preferred
		}
		if choices[i].load != choices[j].load {
			return choices[i].load < choices[j].load
		}
		return choices[i].order < choices[j].order
	})
	last := ErrNotFound
	for _, choice := range choices {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT balanced_candidate`); err != nil {
			return Handle{}, fmt.Errorf("claim: begin balanced candidate: %w", err)
		}
		selected := input
		selected.BalancedCandidates = nil
		selected.Candidate = choice.candidate.Candidate
		selected.DonationKeyID = choice.candidate.DonationKeyID
		selected.OutputTokenFloor = choice.candidate.OutputTokenFloor
		handle, claimErr := s.claimTx(ctx, tx, claimID, at, selected)
		if claimErr == nil {
			if _, err := tx.ExecContext(ctx, `RELEASE balanced_candidate`); err != nil {
				return Handle{}, fmt.Errorf("claim: finish balanced candidate: %w", err)
			}
			return handle, nil
		}
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO balanced_candidate`); err != nil {
			return Handle{}, fmt.Errorf("claim: roll back balanced candidate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `RELEASE balanced_candidate`); err != nil {
			return Handle{}, fmt.Errorf("claim: close balanced candidate: %w", err)
		}
		if errors.Is(claimErr, ErrNotFound) || errors.Is(claimErr, ErrKeyRateLimited) || errors.Is(claimErr, donationquota.ErrLimited) {
			last = claimErr
			continue
		}
		return Handle{}, claimErr
	}
	return Handle{}, last
}

func physicalDispatchLoadTx(ctx context.Context, tx *sql.Tx, physical [32]byte, at int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT dispatch_count FROM charity_dispatch_buckets
WHERE physical_id=? AND dispatched_at>? AND dispatched_at<=?`, physical[:], at-300, at)
	if err != nil {
		return 0, fmt.Errorf("claim: read physical dispatch load: %w", err)
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var count int64
		if err := rows.Scan(&count); err != nil {
			return 0, fmt.Errorf("claim: scan physical dispatch load: %w", err)
		}
		if count < 0 {
			return 0, ErrInvariant
		}
		if count > math.MaxInt64-total {
			total = math.MaxInt64
		} else {
			total += count
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("claim: iterate physical dispatch load: %w", err)
	}
	var pending int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM charity_dispatch_receipts
WHERE physical_id=? AND state='reserved'`, physical[:]).Scan(&pending); err != nil {
		return 0, fmt.Errorf("claim: read pending physical load: %w", err)
	}
	if pending > math.MaxInt64-total {
		return math.MaxInt64, nil
	}
	return total + pending, nil
}

func reserveCharityDispatchTx(ctx context.Context, tx *sql.Tx, claimID string, input ClaimInput, secretRefID int64, baseURL string, at int64) error {
	physical, err := physicalCharityKeyID(input.Candidate.EndpointKeyID, secretRefID, baseURL)
	if err != nil {
		return err
	}
	var modelID, revision int64
	if err := tx.QueryRowContext(ctx, `SELECT r.charity_model_id,s.revision
FROM charity_reservations r JOIN charity_routing_settings s ON s.model_id=r.charity_model_id
WHERE r.logical_request_id=? AND r.user_id=?`, input.RequestID, input.ActorUserID).
		Scan(&modelID, &revision); err != nil {
		return fmt.Errorf("claim: read charity dispatch settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO charity_dispatch_receipts
(attempt_id,physical_id,endpoint_key_id,user_id,model_id,routing_revision,state,reserved_at,expires_at)
VALUES(?,?,?,?,?,?,'reserved',?,?)`, claimID, physical[:], input.Candidate.EndpointKeyID,
		input.ActorUserID, modelID, revision, at, at+300); err != nil {
		return fmt.Errorf("claim: reserve physical dispatch receipt: %w", err)
	}
	return nil
}
