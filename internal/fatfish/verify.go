package fatfish

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func (s *Service) processVerification(ctx context.Context, job *verifyJob) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	c, err := readChallengeTx(ctx, tx, job.id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if c.state != "verifying" {
		tx.Rollback()
		return nil
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	var seed [32]byte
	copy(seed[:], c.seed)
	periodID, nodeID := c.periodID, c.nodeID
	if c.playtest {
		periodID, nodeID = c.versionID, c.versionID
	}
	commit, err := engine.SeedCommitForVersion(c.id, periodID, nodeID, hex.EncodeToString(c.contentHash), c.engineVersion, c.scoringVersion, seed)
	if err != nil || subtle.ConstantTimeCompare([]byte(commit), []byte(hex.EncodeToString(c.seedCommit))) != 1 {
		clear(job.inputs)
		return s.refundVerificationFault(c.id, "commitment_mismatch")
	}
	level, err := engine.ParseLevel([]byte(c.levelJSON.String))
	if err != nil || level.EngineVersion != c.engineVersion || level.ScoringVersion != c.scoringVersion {
		clear(job.inputs)
		return s.refundVerificationFault(c.id, "version_invalid")
	}
	verificationStart := time.Now()
	inputs, parseErr := engine.ParseInputs(job.inputs, level)
	var replay engine.ReplayResult
	if parseErr == nil {
		replay, err = s.replay(level, seed, inputs, engine.ReplayOptions{Context: ctx})
	}
	clear(inputs)
	clear(job.inputs) // No complete trajectory survives the replay, including ledger retries.
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) && s.isClosed() {
			return nil // Keep the accepted digest recoverable after normal shutdown.
		}
		return s.refundVerificationFault(c.id, "verification_timeout")
	}
	if err != nil && parseErr == nil && errors.Is(err, context.Canceled) {
		return s.refundVerificationFault(c.id, "verification_cancelled")
	}
	premature := parseErr == nil && err == nil && c.start.Valid && c.received.Valid && c.received.Int64 < c.start.Int64+int64(replay.TerminalTick)*1000/60
	if parseErr != nil || err != nil || replay.TerminalTick != job.terminalTick || premature {
		reason := "invalid_input"
		if premature {
			reason = "premature_terminal"
		}
		replay = engine.ReplayResult{EngineVersion: c.engineVersion, ScoringVersion: c.scoringVersion,
			ContentHash: hex.EncodeToString(c.contentHash), TerminalTick: 0, Reason: reason, BowlCounts: []engine.BowlState{}, ScoreUnits: 0}
	}
	durationMS := time.Since(verificationStart).Milliseconds()
	if durationMS > 5000 {
		durationMS = 5000
	}
	if err = s.recordVerificationFact(ctx, c, replay, durationMS); err != nil {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) && s.isClosed() {
				return nil
			}
			return s.refundVerificationFault(c.id, "verification_timeout")
		}
		return err
	}
	settleCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.settleVerified(settleCtx, c.id)
}

func (s *Service) refundVerificationFault(id, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.refundSystemFault(ctx, id, reason)
}

func (s *Service) rewardTiersTx(ctx context.Context, tx *sql.Tx, c challengeRow, stars int) ([]rewardTier, *big.Int, error) {
	if stars < 1 || stars > 3 {
		return nil, big.NewInt(0), ErrInvalid
	}
	identity, err := s.identity.UserKeyTx(ctx, tx, c.userID)
	if err != nil {
		return nil, nil, err
	}
	var mags [4][]byte
	err = tx.QueryRowContext(ctx, `SELECT first_clear_reward_mag,star1_reward_mag,star2_reward_mag,star3_reward_mag
 FROM fatfish_node_revisions WHERE node_id=? AND revision=?`, c.nodeID, c.nodeRevision).Scan(&mags[0], &mags[1], &mags[2], &mags[3])
	if err != nil {
		return nil, nil, err
	}
	names := [4]string{"first_clear", "star1", "star2", "star3"}
	tiers := make([]rewardTier, 0, stars+1)
	total := new(big.Int)
	for i := 0; i <= stars; i++ {
		var existing int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_reward_claims WHERE identity_key=? AND period_id=? AND node_id=? AND tier=?`, identity[:], c.periodID, c.nodeID, names[i]).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
		mag, err := db.DecodeU128(mags[i])
		if err != nil {
			return nil, nil, ErrInvariant
		}
		if mag.Big().BitLen() > 127 {
			return nil, nil, ErrInvariant
		}
		total.Add(total, mag.Big())
		tiers = append(tiers, rewardTier{name: names[i], mag: mags[i], identity: identity[:]})
	}
	return tiers, total, nil
}

type rewardTier struct {
	name     string
	mag      []byte
	identity []byte
}

func (s *Service) recordVerificationFact(ctx context.Context, c challengeRow, replay engine.ReplayResult, durationMS int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := readChallengeTx(ctx, tx, c.id)
	if err != nil {
		return err
	}
	if current.state != "verifying" {
		return tx.Commit()
	}
	if current.resultJSON.Valid {
		return tx.Commit()
	}
	charge, err := amountText(current.price)
	if err != nil {
		return err
	}
	result := ResultView{State: "verifying", Reason: replay.Reason, TerminalTick: replay.TerminalTick,
		Fed: replay.Fed, Total: replay.Total, BowlCounts: replay.BowlCounts, Passed: replay.Passed,
		Stars: replay.Stars, ScoreUnits: strconv.FormatInt(replay.ScoreUnits, 10),
		EngineVersion: replay.EngineVersion, ScoringVersion: replay.ScoringVersion,
		ContentHash: replay.ContentHash, FinalStateHash: replay.FinalStateHash,
		SeedCommit: hex.EncodeToString(current.seedCommit), CommitmentVerified: true,
		TicketCharge: charge, TicketRefund: "0", Rewards: "0", VerificationDurationMS: durationMS}
	if result.Passed && !c.playtest {
		_, total, err := s.rewardTiersTx(ctx, tx, current, result.Stars)
		if err != nil {
			return err
		}
		result.Rewards = amountMilliText(total)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(raw) > 16384 {
		return ErrInvariant
	}
	res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET verified_result_json=?,revision=revision+1
 WHERE id=? AND state='verifying' AND verified_result_json IS NULL AND revision=?`, string(raw), c.id, current.revision)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

func amountMilliText(value *big.Int) string {
	q, r := new(big.Int).QuoRem(value, big.NewInt(1000), new(big.Int))
	if r.Sign() == 0 {
		return q.String()
	}
	f := strconv.FormatInt(r.Int64()+1000, 10)[1:]
	for len(f) > 0 && f[len(f)-1] == '0' {
		f = f[:len(f)-1]
	}
	return q.String() + "." + f
}

func (s *Service) settleVerified(ctx context.Context, id string) error {
	nowMS, err := s.nowMS()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if c.state != "verifying" || !c.resultJSON.Valid {
		return tx.Commit()
	}
	var result ResultView
	if err = json.Unmarshal([]byte(c.resultJSON.String), &result); err != nil {
		return ErrInvariant
	}
	if result.ContentHash != hex.EncodeToString(c.contentHash) || result.SeedCommit != hex.EncodeToString(c.seedCommit) || !result.CommitmentVerified || result.EngineVersion != c.engineVersion || result.ScoringVersion != c.scoringVersion {
		return ErrInvariant
	}
	if c.playtest {
		state := "settled_fail"
		if result.Passed {
			state = "settled_pass"
		}
		playtestID, err := db.GenerateOpaqueID("fpt_")
		if err != nil {
			return err
		}
		var actor int64
		if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? AND is_admin=1`, c.userID).Scan(&actor); err != nil {
			return err
		}
		score, err := strconv.ParseInt(result.ScoreUnits, 10, 64)
		if err != nil {
			return ErrInvariant
		}
		if !result.Passed {
			score = 0
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_playtests(id,version_id,challenge_id,actor_user_id,passed,stars,score_units,duration_ms,result_json,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?)`, playtestID, c.versionID, c.id, c.userID, boolInt(result.Passed), result.Stars, score, result.VerificationDurationMS, c.resultJSON.String, nowMS/1000)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state=?,terminal_at_ms=?,terminal_reason=?,revision=revision+1 WHERE id=? AND state='verifying' AND revision=?`, state, nowMS, result.Reason, c.id, c.revision)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	var banned int
	var bannedUntil sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT is_banned,banned_until FROM users WHERE id=?`, c.userID).Scan(&banned, &bannedUntil); err != nil {
		return err
	}
	if banned != 0 && (!bannedUntil.Valid || bannedUntil.Int64 > nowMS/1000) {
		if err = s.cancelChallengeTx(ctx, tx, c, nowMS, "account_banned", true); err != nil {
			return err
		}
		return tx.Commit()
	}
	if result.Passed {
		if err = s.applyProgressAndRewardsTx(ctx, tx, c, result, nowMS); err != nil {
			return err
		}
	}
	state := "settled_fail"
	if result.Passed {
		state = "settled_pass"
	}
	res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state=?,terminal_at_ms=?,terminal_reason=?,revision=revision+1
 WHERE id=? AND state='verifying' AND revision=?`, state, nowMS, result.Reason, id, c.revision)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Service) applyProgressAndRewardsTx(ctx context.Context, tx *sql.Tx, c challengeRow, result ResultView, nowMS int64) error {
	score, err := strconv.ParseInt(result.ScoreUnits, 10, 64)
	if err != nil || score <= 0 || score > 100000000 || result.Stars < 1 || result.Stars > 3 {
		return ErrInvariant
	}
	identity, err := s.identity.UserKeyTx(ctx, tx, c.userID)
	if err != nil {
		return err
	}
	var mags [4][]byte
	err = tx.QueryRowContext(ctx, `SELECT first_clear_reward_mag,star1_reward_mag,star2_reward_mag,star3_reward_mag
 FROM fatfish_node_revisions WHERE node_id=? AND revision=?`, c.nodeID, c.nodeRevision).Scan(&mags[0], &mags[1], &mags[2], &mags[3])
	if err != nil {
		return err
	}
	names := [4]string{"first_clear", "star1", "star2", "star3"}
	totalReward := new(big.Int)
	for i := 0; i <= result.Stars; i++ {
		name := names[i]
		var existing int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_reward_claims WHERE identity_key=? AND period_id=? AND node_id=? AND tier=?`, identity[:], c.periodID, c.nodeID, name).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		amount, err := amountFromMag(mags[i])
		if err != nil {
			return err
		}
		var opID sql.NullString
		if !amount.IsZero() {
			user, err := ledger.UserAccount(ctx, tx, c.userID)
			if err != nil {
				return err
			}
			external, err := ledger.CodedAccount(ctx, tx, "external")
			if err != nil {
				return err
			}
			id, err := db.GenerateOpaqueID("op_")
			if err != nil {
				return err
			}
			plan, err := ledger.NewFatFishReward(ledger.Meta{OperationID: id, CreatedAt: nowMS / 1000}, external.ID, user.ID, amount)
			if err != nil {
				return err
			}
			if _, err = ledger.Apply(ctx, tx, plan); err != nil {
				return err
			}
			opID = sql.NullString{String: id, Valid: true}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_reward_claims(identity_key,period_id,node_id,tier,operation_id,amount_mag,created_at)
 VALUES(?,?,?,?,?,?,?)`, identity[:], c.periodID, c.nodeID, name, opID, mags[i], nowMS/1000)
		if err != nil {
			return err
		}
		totalReward.Add(totalReward, amount.Big())
	}
	if result.Rewards != amountMilliText(totalReward) {
		return ErrConflict
	}
	var oldStars int
	var oldScore int64
	var bestAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT best_stars,best_score_units,best_at_ms FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?`, c.userID, c.periodID, c.nodeID).Scan(&oldStars, &oldScore, &bestAt)
	if err != nil {
		return err
	}
	newStars := oldStars
	if result.Stars > newStars {
		newStars = result.Stars
	}
	if score > oldScore {
		if !c.start.Valid {
			return ErrInvariant
		}
		bestAt := c.start.Int64 + int64(result.TerminalTick)*1000/60
		_, err = tx.ExecContext(ctx, `UPDATE fatfish_progress SET passed=1,best_stars=?,best_score_units=?,best_version_id=?,best_at_ms=?,best_challenge_id=?,best_confirmed_at_ms=?
 WHERE user_id=? AND period_id=? AND node_id=?`, newStars, score, c.versionID, bestAt, c.id, nowMS, c.userID, c.periodID, c.nodeID)
	} else if newStars > oldStars {
		_, err = tx.ExecContext(ctx, `UPDATE fatfish_progress SET best_stars=? WHERE user_id=? AND period_id=? AND node_id=?`, newStars, c.userID, c.periodID, c.nodeID)
	}
	if err != nil {
		return err
	}
	var total int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(best_score_units),0) FROM fatfish_progress WHERE user_id=? AND period_id=?`, c.userID, c.periodID).Scan(&total)
	if err != nil {
		return err
	}
	var currentTotal int64
	var achieved int64
	err = tx.QueryRowContext(ctx, `SELECT total_score_units,achieved_at_ms FROM fatfish_period_progress WHERE user_id=? AND period_id=?`, c.userID, c.periodID).Scan(&currentTotal, &achieved)
	if errors.Is(err, sql.ErrNoRows) {
		var tie [16]byte
		if _, err = io.ReadFull(s.random, tie[:]); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_period_progress(user_id,period_id,total_score_units,achieved_at_ms,public_tie_key) VALUES(?,?,?,?,?)`, c.userID, c.periodID, total, nowMS, tie[:])
	} else if err == nil && total > currentTotal {
		_, err = tx.ExecContext(ctx, `UPDATE fatfish_period_progress SET total_score_units=?,achieved_at_ms=? WHERE user_id=? AND period_id=?`, total, nowMS, c.userID, c.periodID)
	}
	return err
}
