package stewardautomation

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type ownedKey struct{ endpointID, keyID int64 }

func ownKeyTx(ctx context.Context, tx *sql.Tx, userID, donationKeyID, now int64) (ownedKey, error) {
	var key ownedKey
	err := tx.QueryRowContext(ctx, `SELECT e.id,k.id FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
JOIN endpoint_keys k ON k.id=m.endpoint_key_id JOIN endpoints e ON e.id=k.endpoint_id
WHERE dk.id=? AND d.user_id=? AND e.user_id=? AND d.status='approved' AND dk.ended_at IS NULL
AND (dk.expires_at IS NULL OR dk.expires_at>?) AND (dk.authorized_expires_at IS NULL OR dk.authorized_expires_at>?)`, donationKeyID, userID, userID, now, now).Scan(&key.endpointID, &key.keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return ownedKey{}, resources.ErrNotFound
	}
	return key, err
}

func (s *Service) bind(ctx context.Context, userID int64, input bindingInput) (bindingsResult, int, error) {
	modelID, err := numericID(input.CharityModelID)
	if err != nil || len(input.DonationKeyIDs) < 1 || len(input.DonationKeyIDs) > maxKeys || !validModelID(input.UpstreamModelID) {
		return bindingsResult{}, 0, errInvalid
	}
	ids := make([]int64, len(input.DonationKeyIDs))
	seen := make(map[int64]bool, len(ids))
	for index, value := range input.DonationKeyIDs {
		id, err := numericID(value)
		if err != nil || seen[id] {
			return bindingsResult{}, 0, errInvalid
		}
		ids[index], seen[id] = id, true
	}
	tx, err := s.begin(ctx, userID)
	if err != nil {
		return bindingsResult{}, 0, err
	}
	var present bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_models WHERE id=?)`, modelID).Scan(&present)
	_ = tx.Rollback()
	if err != nil {
		return bindingsResult{}, 0, err
	}
	if !present {
		return bindingsResult{}, 0, resources.ErrNotFound
	}
	out, status := runBindings(ctx, input, ids, func(ctx context.Context, id int64) error {
		return s.bindOne(ctx, userID, modelID, id, input.UpstreamModelID, input.Manual)
	})
	return out, status, nil
}

func runBindings(ctx context.Context, input bindingInput, ids []int64, bindOne func(context.Context, int64) error) (bindingsResult, int) {
	out := bindingsResult{CharityModelID: input.CharityModelID, Results: make([]bindingResult, len(ids))}
	successes, incomplete := 0, false
	var stopped error
	for index, id := range ids {
		result := bindingResult{DonationKeyID: input.DonationKeyIDs[index], Status: "success"}
		err := stopped
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = bindOne(ctx, id)
		}
		if err == nil {
			successes++
		} else {
			result.Status = "failed"
			result.Code, result.Message = safeError(err)
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				result.Status, incomplete = "incomplete", true
			}
			if result.Code == httperr.CodeUnauthorized || result.Code == httperr.CodeForbidden {
				stopped = err
			}
		}
		out.Results[index] = result
	}
	if successes > 0 {
		return out, http.StatusOK
	}
	if incomplete {
		return out, http.StatusGatewayTimeout
	}
	return out, http.StatusUnprocessableEntity
}

func (s *Service) bindOne(ctx context.Context, userID, modelID, donationKeyID int64, upstreamModelID string, manual bool) error {
	tx, err := s.begin(ctx, userID)
	if err != nil {
		return err
	}
	key, err := ownKeyTx(ctx, tx, userID, donationKeyID, time.Now().Unix())
	_ = tx.Rollback()
	if err != nil {
		return err
	}
	var revision int64
	if !manual {
		discoveryContext, cancel := context.WithTimeout(ctx, s.discoveryTimeout)
		revision, err = s.resources.RefreshDiscoveryAndWait(discoveryContext, userID, key.endpointID, key.keyID)
		cancel()
		if err != nil {
			return err
		}
	}
	tx, err = s.begin(ctx, userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := ownKeyTx(ctx, tx, userID, donationKeyID, time.Now().Unix())
	if err != nil {
		return err
	}
	if current != key {
		return resources.ErrConflict
	}
	if manual {
		if err := s.resources.EnsureManualEntryInTransaction(ctx, tx, userID, key.endpointID, key.keyID, upstreamModelID); err != nil {
			return err
		}
	} else if err := freshModelTx(ctx, tx, key.keyID, revision, upstreamModelID); err != nil {
		return err
	}
	if err := s.charity.EnsureOwnBindingInTransaction(ctx, tx, userID, modelID, donationKeyID, upstreamModelID, revision); err != nil {
		return err
	}
	return tx.Commit()
}

func freshModelTx(ctx context.Context, tx *sql.Tx, keyID, revision int64, modelID string) error {
	var current int64
	var state, class string
	if err := tx.QueryRowContext(ctx, `SELECT revision,state,safe_class FROM model_discovery_evidence WHERE endpoint_key_id=?`, keyID).Scan(&current, &state, &class); err != nil {
		return err
	}
	if current != revision {
		return resources.ErrConflict
	}
	if state != "succeeded" {
		return &discoveryFailureError{class: class}
	}
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_pair_catalog WHERE endpoint_key_id=? AND normalized_model_id=? AND automatic_revision=? AND automatic_supports>0)`, keyID, modelID, revision).Scan(&present); err != nil {
		return err
	}
	if !present {
		return errModelMissing
	}
	return nil
}
