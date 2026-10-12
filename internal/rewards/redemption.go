package rewards

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

type redemptionResult struct {
	response  Redemption
	rejection error
	violation bool
	campaign  string
}

// Redeem commits the receipt, submission replay and abuse decision together.
func (s *Service) Redeem(ctx context.Context, user int64, key, code string) (Redemption, error) {
	if _, err := idempotency.KeyHash(key); err != nil {
		return Redemption{}, ErrInvalid
	}
	if s.abuse == nil {
		return Redemption{}, errors.New("reward restriction service is unavailable")
	}
	actor, err := idempotency.ActorScopeHash("user", strconv.FormatInt(user, 10))
	if err != nil {
		return Redemption{}, err
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	digest, err := s.redemptionDigest(actor, code)
	if err != nil {
		return Redemption{}, err
	}
	var result redemptionResult
	err = s.abuse.Redeem(ctx, user, func(ctx context.Context, tx *sql.Tx, at, replaySeconds int64) (antiabuse.RedemptionAttempt, error) {
		if err := s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
			return antiabuse.RedemptionAttempt{}, err
		}
		if err := s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
			return antiabuse.RedemptionAttempt{}, err
		}
		decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.ScopeRewardRedemption, ActorHash: actor, Key: key, RequestHash: digest, DecisionNow: at, ReplaySeconds: replaySeconds})
		if err != nil {
			return antiabuse.RedemptionAttempt{}, err
		}
		if decision.Kind == idempotency.Replay {
			result, err = replayRedemption(decision)
			return antiabuse.RedemptionAttempt{Replay: true}, err
		}
		// Count committed new submissions, including business refusals. A
		// replay never consumes the minute limit or a second abuse event.
		var attempts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (
 SELECT 1 FROM idempotency_records WHERE scope='reward_redemption' AND actor_scope_hash=? AND created_at>=? LIMIT 13
)`, actor[:], at-at%60).Scan(&attempts); err != nil {
			return antiabuse.RedemptionAttempt{}, err
		}
		if attempts > 12 {
			return antiabuse.RedemptionAttempt{}, ErrRate
		}
		result, err = s.redeemTx(ctx, tx, user, code, at)
		if err != nil {
			return antiabuse.RedemptionAttempt{}, err
		}
		return antiabuse.RedemptionAttempt{
			Violation: result.violation, GraceCampaign: result.campaign,
			Complete: func(ctx context.Context, tx *sql.Tx, bannedUntil int64) error {
				if bannedUntil > 0 {
					result = redemptionResult{response: Redemption{BannedUntil: &bannedUntil}}
				}
				body, err := json.Marshal(redemptionResponse(result.response, result.rejection))
				if err != nil {
					return err
				}
				return idempotency.Complete(ctx, tx, decision, http.StatusOK, body)
			},
		}, nil
	})
	if err != nil {
		return Redemption{}, err
	}
	return result.response, result.rejection
}

func (s *Service) redemptionDigest(actor [32]byte, code string) ([32]byte, error) {
	key, err := s.config.Keys.DeriveGenerationTwoSubkey([]byte("reward-redemption-request/v1"))
	if err != nil {
		return [32]byte{}, err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(code))
	return idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: http.MethodPost, Route: "/api/rewards/redeem", Body: mac.Sum(nil)})
}

func (s *Service) redeemTx(ctx context.Context, tx *sql.Tx, user int64, code string, at int64) (redemptionResult, error) {
	if _, err := normalizeCode(code); err != nil {
		return redemptionResult{rejection: ErrUnavailable, violation: true}, nil
	}
	var id string
	var actor sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT id,actor_user_id FROM reward_campaigns WHERE code=? AND kind='code'", code).Scan(&id, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return redemptionResult{rejection: ErrUnavailable, violation: true}, nil
	}
	if err != nil {
		return redemptionResult{}, err
	}
	c, err := readConfiguration(ctx, tx, id)
	if err != nil {
		return redemptionResult{}, err
	}
	var claimID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM reward_claims WHERE campaign_id=? AND user_id=? AND status='success'", id, user).Scan(&claimID)
	if err == nil {
		receipt, err := readReceipt(ctx, tx, user, claimID)
		return redemptionResult{response: Redemption{Receipt: receipt, AlreadyClaimed: true}, violation: true}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return redemptionResult{}, err
	}
	if !c.Enabled || c.StartsAt != nil && at < *c.StartsAt || c.EndsAt != nil && at >= *c.EndsAt || c.Quota != nil && c.Claimed >= *c.Quota {
		return redemptionResult{rejection: ErrUnavailable, campaign: id}, nil
	}
	f, err := loadFacts(ctx, tx, user, at, usesAssets(c.Conditions))
	if err != nil {
		return redemptionResult{}, err
	}
	if !matches(c.Conditions, f) {
		return redemptionResult{rejection: ErrConditions, campaign: id}, nil
	}
	// A capacity refusal keeps its replay result without counting as abuse;
	// every item in the reward bundle must roll back together.
	if _, err := tx.ExecContext(ctx, "SAVEPOINT reward_bundle"); err != nil {
		return redemptionResult{}, err
	}
	results, err := grant(ctx, tx, user, actor.Int64, at, c.Title, c.Rewards)
	if errors.Is(err, ErrCapacity) {
		if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO reward_bundle"); rollbackErr != nil {
			return redemptionResult{}, rollbackErr
		}
		if _, releaseErr := tx.ExecContext(ctx, "RELEASE reward_bundle"); releaseErr != nil {
			return redemptionResult{}, releaseErr
		}
		return redemptionResult{rejection: ErrCapacity}, nil
	}
	if err != nil {
		return redemptionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE reward_bundle"); err != nil {
		return redemptionResult{}, err
	}
	r, err := tx.ExecContext(ctx, "INSERT INTO reward_claims(campaign_id,user_id,status,receipt,created_at,completed_at) VALUES(?,?,'success',?,?,?)", id, user, jsonText(results), at, at)
	if err != nil {
		return redemptionResult{}, err
	}
	claimID, err = r.LastInsertId()
	if err != nil {
		return redemptionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE reward_campaigns SET claimed=claimed+1 WHERE id=?", id); err != nil {
		return redemptionResult{}, err
	}
	if s.config.Activity != nil {
		if err := s.config.Activity.RecordLimitedActivityTx(ctx, tx, user, at); err != nil {
			return redemptionResult{}, err
		}
	}
	receipt, err := readReceipt(ctx, tx, user, claimID)
	return redemptionResult{response: Redemption{Receipt: receipt}}, err
}

func redemptionResponse(out Redemption, err error) any {
	if out.BannedUntil != nil {
		return struct {
			Rejection   string `json:"rejection"`
			BannedUntil int64  `json:"banned_until"`
		}{"banned", *out.BannedUntil}
	}
	for _, item := range []struct {
		err  error
		code string
	}{{ErrUnavailable, "unavailable"}, {ErrConditions, "conditions"}, {ErrCapacity, "capacity"}} {
		if errors.Is(err, item.err) {
			return map[string]string{"rejection": item.code}
		}
	}
	return out
}

func replayRedemption(decision idempotency.Decision) (redemptionResult, error) {
	var response struct {
		Redemption
		Rejection string `json:"rejection"`
	}
	if decision.HTTPStatus != http.StatusOK || json.Unmarshal(decision.ResponseBody, &response) != nil {
		return redemptionResult{}, idempotency.ErrState
	}
	result := redemptionResult{response: response.Redemption}
	switch response.Rejection {
	case "":
	case "banned":
		if response.BannedUntil == nil {
			return redemptionResult{}, idempotency.ErrState
		}
	case "unavailable":
		result.rejection = ErrUnavailable
	case "conditions":
		result.rejection = ErrConditions
	case "capacity":
		result.rejection = ErrCapacity
	default:
		return redemptionResult{}, idempotency.ErrState
	}
	return result, nil
}
