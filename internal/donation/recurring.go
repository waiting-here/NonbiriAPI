package donation

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const (
	routeOwnerRecurring                = "/api/donations/{id}/keys/{keyId}/recurring-limits"
	routeAdminRecurring                = "/admin/api/donations/{id}/keys/{keyId}/recurring-limits"
	routeStewardRecurring              = "/api/steward/donations/{id}/keys/{keyId}/recurring-limits"
	recurringOwner        reviewerRole = "owner"
)

type RecurringReceipt struct {
	DonationID       string `json:"donation_id"`
	KeyID            string `json:"key_id"`
	DonationRevision string `json:"donation_revision"`
}

type RecurringLimits struct {
	RecurringReceipt
	ServerNow int64                    `json:"server_now"`
	Rules     []donationquota.RuleView `json:"rules"`
}

func (s *Service) RecurringOwner(ctx context.Context, userID, donationID, keyID int64) (RecurringLimits, error) {
	return s.recurring(ctx, recurringOwner, userID, donationID, keyID)
}
func (s *Service) RecurringAdmin(ctx context.Context, donationID, keyID int64) (RecurringLimits, error) {
	return s.recurring(ctx, reviewerAdmin, 0, donationID, keyID)
}
func (s *Service) RecurringSteward(ctx context.Context, userID, donationID, keyID int64) (RecurringLimits, error) {
	return s.recurring(ctx, reviewerSteward, userID, donationID, keyID)
}

func (s *Service) recurring(ctx context.Context, role reviewerRole, userID, donationID, keyID int64) (RecurringLimits, error) {
	if ctx == nil || donationID <= 0 || keyID <= 0 {
		return RecurringLimits{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var tx *sql.Tx
	var err error
	if role == recurringOwner {
		tx, err = s.beginOwnerTx(ctx, userID)
	} else {
		tx, _, err = s.beginRoleTx(ctx, role, userID)
	}
	if err != nil {
		return RecurringLimits{}, err
	}
	defer tx.Rollback()
	now, err := s.nowUnix()
	if err != nil {
		return RecurringLimits{}, err
	}
	revision, err := s.recurringScope(ctx, tx, role, userID, donationID, keyID, now)
	if err != nil {
		return RecurringLimits{}, err
	}
	rules, err := donationquota.Views(ctx, tx, keyID, now)
	if err != nil {
		return RecurringLimits{}, quotaError(err)
	}
	if err := tx.Commit(); err != nil {
		return RecurringLimits{}, err
	}
	return RecurringLimits{RecurringReceipt: recurringReceipt(donationID, keyID, revision), ServerNow: now, Rules: rules}, nil
}

func (s *Service) recurringScope(ctx context.Context, tx *sql.Tx, role reviewerRole, userID, donationID, keyID, now int64) (int64, error) {
	if role == recurringOwner {
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donations WHERE id=? AND user_id=?)`, donationID, userID).Scan(&owned); err != nil {
			return 0, err
		}
		if !owned {
			return 0, ErrNotFound
		}
		visible, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
		if err != nil {
			return 0, err
		}
		if !visible {
			return 0, ErrNotFound
		}
	} else if err := requireManagedDonationTx(ctx, tx, role, donationID, now); err != nil {
		return 0, err
	}
	if role == reviewerAdmin {
		ordinary, err := donationOrdinarilyVisibleTx(ctx, tx, donationID, now)
		if err != nil {
			return 0, err
		}
		if !ordinary {
			if s.heldRead == nil {
				return 0, ErrNotFound
			}
			held, err := s.heldRead.AuthorizeHeldDonationRead(ctx, tx, donationID, now)
			if err != nil {
				return 0, err
			}
			if !held {
				return 0, ErrNotFound
			}
		}
	}
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT d.revision FROM donations d JOIN donation_keys k ON k.donation_id=d.id WHERE d.id=? AND k.id=?`, donationID, keyID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return revision, err
}

func recurringReceipt(donationID, keyID, revision int64) RecurringReceipt {
	return RecurringReceipt{DonationID: strconv.FormatInt(donationID, 10), KeyID: strconv.FormatInt(keyID, 10), DonationRevision: strconv.FormatInt(revision, 10)}
}

func (s *Service) ReplaceRecurringAdmin(ctx context.Context, donationID, keyID int64, mutation resources.ControlMutation, expected int64, rules []donationquota.RuleInput) (resources.MutationResult[RecurringReceipt], error) {
	return s.replaceRecurring(ctx, reviewerAdmin, 0, donationID, keyID, mutation, expected, rules)
}
func (s *Service) ReplaceRecurringSteward(ctx context.Context, userID, donationID, keyID int64, mutation resources.ControlMutation, expected int64, rules []donationquota.RuleInput) (resources.MutationResult[RecurringReceipt], error) {
	return s.replaceRecurring(ctx, reviewerSteward, userID, donationID, keyID, mutation, expected, rules)
}

func (s *Service) replaceRecurring(ctx context.Context, role reviewerRole, userID, donationID, keyID int64, mutation resources.ControlMutation, expected int64, rules []donationquota.RuleInput) (resources.MutationResult[RecurringReceipt], error) {
	var empty resources.MutationResult[RecurringReceipt]
	route := routeAdminRecurring
	if role == reviewerSteward {
		route = routeStewardRecurring
	}
	if ctx == nil || donationID <= 0 || keyID <= 0 || expected <= 0 || len(rules) > donationquota.MaxRules || !validMutation(mutation, http.MethodPut, route, donationID, keyID) {
		return empty, ErrInvalidRequest
	}
	for _, rule := range rules {
		if donationquota.Validate(rule) != nil {
			return empty, ErrInvalidRequest
		}
	}
	tx, actorID, err := s.beginRoleTx(ctx, role, userID)
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if _, err := s.recurringScope(ctx, tx, role, userID, donationID, keyID, now); err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, string(role), actorID, idempotency.ScopeControlMutation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[RecurringReceipt](decision)
	}
	// Pending keys may be configured before review. Logical expiry or another
	// writer's revision prevents a stale command from changing the set.
	result, err := tx.ExecContext(ctx, `UPDATE donations SET revision=revision+1,updated_at=? WHERE id=? AND revision=? AND revision<9223372036854775807 AND status IN ('pending','approved') AND EXISTS(SELECT 1 FROM donation_keys k WHERE k.id=? AND k.donation_id=donations.id AND k.ended_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>?))`, now, donationID, expected, keyID, now)
	if err != nil {
		return empty, err
	}
	if err := requireOne(result); err != nil {
		return empty, err
	}
	if err := donationquota.Replace(ctx, tx, keyID, now, rules); err != nil {
		return empty, quotaError(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at) VALUES(?,?,?,?,'limit_update','',?)`, donationID, expected+1, actorID, string(role), now)
	if err != nil {
		return empty, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, recurringReceipt(donationID, keyID, expected+1))
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

func quotaError(err error) error {
	switch {
	case errors.Is(err, donationquota.ErrInvalid):
		return ErrInvalidRequest
	case errors.Is(err, donationquota.ErrConflict):
		return ErrConflict
	case errors.Is(err, donationquota.ErrCapacity):
		return ErrUnavailable
	case errors.Is(err, donationquota.ErrInvariant):
		return ErrInvariant
	default:
		return err
	}
}
