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

const routeOwnerFailurePolicy = "/api/donations/{id}/keys/{keyId}/failure-policy"
const routeAdminFailurePolicy = "/admin/api/donations/{id}/keys/{keyId}/failure-policy"
const routeStewardFailurePolicy = "/api/steward/donations/{id}/keys/{keyId}/failure-policy"

// FailurePolicy contains only key policy facts, never source credentials or
// donor identity. Revision belongs to the containing donation.
type FailurePolicy struct {
	DonationID              string `json:"donation_id"`
	DonationKeyID           string `json:"donation_key_id"`
	FailureDisableThreshold string `json:"failure_disable_threshold"`
	FailureStreak           string `json:"failure_streak"`
	FailureDisabled         bool   `json:"failure_disabled"`
	Revision                string `json:"revision"`
}

func readFailurePolicyTx(ctx context.Context, tx *sql.Tx, donationID, keyID int64) (FailurePolicy, error) {
	var out FailurePolicy
	var streak []byte
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT dk.failure_disable_threshold,dk.failure_streak,dk.failure_disabled,d.revision
FROM donation_keys dk JOIN donations d ON d.id=dk.donation_id WHERE dk.id=? AND dk.donation_id=?`, keyID, donationID).
		Scan(&out.FailureDisableThreshold, &streak, &out.FailureDisabled, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	_, err = db.ParseU128Decimal(out.FailureDisableThreshold)
	if err != nil {
		return out, err
	}
	out.FailureStreak, err = decimalFromBlob(streak)
	if err != nil {
		return out, err
	}
	out.DonationID, out.DonationKeyID, out.Revision = strconv.FormatInt(donationID, 10), strconv.FormatInt(keyID, 10), strconv.FormatInt(revision, 10)
	return out, nil
}

func updateFailurePolicyTx(ctx context.Context, tx *sql.Tx, actorID int64, role string, donationID, keyID, expected int64, threshold string, now int64) (FailurePolicy, error) {
	wide, err := db.ParseU128Decimal(threshold)
	if err != nil || expected <= 0 || expected == math.MaxInt64 {
		return FailurePolicy{}, ErrInvalidRequest
	}
	before, err := readFailurePolicyTx(ctx, tx, donationID, keyID)
	if err != nil {
		return FailurePolicy{}, err
	}
	if before.Revision != strconv.FormatInt(expected, 10) {
		return FailurePolicy{}, ErrConflict
	}
	count, err := db.ParseU128Decimal(before.FailureStreak)
	if err != nil {
		return FailurePolicy{}, ErrInvariant
	}
	disabled := wide != (db.U128{}) && count.Big().Cmp(wide.Big()) >= 0
	result, err := tx.ExecContext(ctx, `UPDATE donation_keys SET failure_disable_threshold=?,failure_disabled=?,updated_at=? WHERE id=? AND donation_id=?`, threshold, boolInt(disabled), now, keyID, donationID)
	if err != nil {
		return FailurePolicy{}, err
	}
	if err := requireOne(result); err != nil {
		return FailurePolicy{}, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE donations SET revision=revision+1,updated_at=? WHERE id=? AND revision=?`, now, donationID, expected)
	if err != nil {
		return FailurePolicy{}, err
	}
	if err := requireOne(result); err != nil {
		return FailurePolicy{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,?,?,'failure_policy_update',?,?)`, donationID, expected+1, actorID, role, "failure_disable_threshold="+threshold, now); err != nil {
		return FailurePolicy{}, err
	}
	if disabled && !before.FailureDisabled {
		ref := fmt.Sprintf("donation-key:%d:policy:%d", keyID, expected+1)
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved) VALUES('donation_failure_disabled','charity donation key disabled after consecutive protocol failures',?,?,0)`, ref, now); err != nil {
			return FailurePolicy{}, err
		}
	}
	return readFailurePolicyTx(ctx, tx, donationID, keyID)
}

func (s *Service) failurePolicy(ctx context.Context, actorID int64, role reviewerRole, donationID, keyID, expected int64, threshold string, mutation resources.ControlMutation) (resources.MutationResult[FailurePolicy], error) {
	var empty resources.MutationResult[FailurePolicy]
	route, actorKind := routeOwnerFailurePolicy, "user"
	if role == reviewerAdmin {
		route, actorKind = routeAdminFailurePolicy, string(role)
	}
	if role == reviewerSteward {
		route, actorKind = routeStewardFailurePolicy, string(role)
	}
	if ctx == nil || expected <= 0 || !validMutation(mutation, http.MethodPatch, route, donationID, keyID) {
		return empty, ErrInvalidRequest
	}
	if _, err := db.ParseU128Decimal(threshold); err != nil {
		return empty, ErrInvalidRequest
	}
	var tx *sql.Tx
	var err error
	if role == "" {
		tx, err = s.beginOwnerTx(ctx, actorID)
	} else {
		tx, actorID, err = s.beginRoleTx(ctx, role, actorID)
	}
	if err != nil {
		return empty, err
	}
	committed := false
	defer finishTx(tx, &committed)
	now, err := s.nowUnix()
	if err != nil {
		return empty, err
	}
	if role == "" {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM donations WHERE id=? AND user_id=? AND (status IN ('pending','approved') OR terminal_at>?)`, donationID, actorID, now-terminalRetention).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return empty, ErrNotFound
		}
		if err != nil {
			return empty, err
		}
	} else if err := requireManagedDonationTx(ctx, tx, role, donationID, now); err != nil {
		return empty, err
	}
	if _, err := readFailurePolicyTx(ctx, tx, donationID, keyID); err != nil {
		return empty, err
	}
	decision, err := beginMutation(ctx, tx, actorKind, actorID, idempotency.ScopeDonation, mutation, now)
	if err != nil {
		return empty, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[FailurePolicy](decision)
	}
	value, err := updateFailurePolicyTx(ctx, tx, actorID, string(role), donationID, keyID, expected, threshold, now)
	if err != nil {
		return empty, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, value)
	if err != nil {
		return empty, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return empty, err
	}
	return out, nil
}

// ReadFailurePolicyStewardInTransaction rechecks the role and ordinary history
// boundary. The CallerKey owner also rechecks its generation in this transaction.
func (s *Service) ReadFailurePolicyStewardInTransaction(ctx context.Context, tx *sql.Tx, actorID, donationID, keyID int64) (FailurePolicy, error) {
	if s == nil || ctx == nil || tx == nil || actorID <= 0 || donationID <= 0 || keyID <= 0 {
		return FailurePolicy{}, ErrInvalidRequest
	}
	if err := s.roleAuth.AuthorizeStewardMutation(ctx, tx, actorID); err != nil {
		return FailurePolicy{}, mapAuthorization(err)
	}
	now, err := s.nowUnix()
	if err != nil {
		return FailurePolicy{}, err
	}
	if err := requireManagedDonationTx(ctx, tx, reviewerSteward, donationID, now); err != nil {
		return FailurePolicy{}, err
	}
	return readFailurePolicyTx(ctx, tx, donationID, keyID)
}

func (s *Service) SetFailurePolicyStewardInTransaction(ctx context.Context, tx *sql.Tx, actorID, donationID, keyID, expected int64, threshold string) (FailurePolicy, error) {
	if _, err := s.ReadFailurePolicyStewardInTransaction(ctx, tx, actorID, donationID, keyID); err != nil {
		return FailurePolicy{}, err
	}
	now, err := s.nowUnix()
	if err != nil {
		return FailurePolicy{}, err
	}
	return updateFailurePolicyTx(ctx, tx, actorID, string(reviewerSteward), donationID, keyID, expected, threshold, now)
}
