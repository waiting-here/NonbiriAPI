package fatfish

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
)

type PrepareInput struct {
	PeriodID          string `json:"period_id"`
	NodeID            string `json:"node_id"`
	ExpectedRevision  string `json:"expected_revision"`
	TabCapabilityHash string `json:"tab_capability_hash"`
}

type nodeSnapshot struct {
	periodID, nodeID, versionID                         string
	revision                                            int64
	condition                                           Condition
	hidden                                              bool
	price, unlockCost, firstReward, star1, star2, star3 []byte
	contentHash                                         []byte
	levelJSON                                           string
	durationSeconds                                     int
	engineVersion, scoringVersion                       int
	maximumStars                                        int
	periodState                                         string
	periodVisible, periodPaused                         bool
	periodStart, periodEnd                              int64
}

func readNodeSnapshotTx(ctx context.Context, tx *sql.Tx, periodID, nodeID string) (nodeSnapshot, error) {
	var n nodeSnapshot
	var conditionJSON string
	var hidden, visible, paused int
	err := tx.QueryRowContext(ctx, `SELECT n.period_id,n.id,n.current_revision,r.version_id,r.condition_json,
 r.hidden_until_eligible,r.unlock_cost_mag,r.ticket_price_mag,r.first_clear_reward_mag,
 r.star1_reward_mag,r.star2_reward_mag,r.star3_reward_mag,v.content_hash,v.content_json,
 v.duration_seconds,v.maximum_stars,v.engine_version,v.scoring_version,p.state,p.visible,p.paused,p.starts_at,p.ends_at
 FROM fatfish_nodes n JOIN fatfish_node_revisions r ON r.node_id=n.id AND r.revision=n.current_revision
 JOIN fatfish_level_versions v ON v.id=r.version_id JOIN fatfish_periods p ON p.id=n.period_id
 WHERE n.period_id=? AND n.id=?`, periodID, nodeID).Scan(&n.periodID, &n.nodeID, &n.revision, &n.versionID, &conditionJSON,
		&hidden, &n.unlockCost, &n.price, &n.firstReward, &n.star1, &n.star2, &n.star3,
		&n.contentHash, &n.levelJSON, &n.durationSeconds, &n.maximumStars, &n.engineVersion, &n.scoringVersion,
		&n.periodState, &visible, &paused, &n.periodStart, &n.periodEnd)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, err
	}
	n.hidden = hidden == 1
	n.periodVisible = visible == 1
	n.periodPaused = paused == 1
	n.condition, err = ParseCondition([]byte(conditionJSON))
	if err != nil {
		return n, ErrInvariant
	}
	return n, nil
}

func bestStarsTx(ctx context.Context, tx *sql.Tx, userID int64, periodID string) (map[string]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT node_id,best_stars FROM fatfish_progress WHERE user_id=? AND period_id=?`, userID, periodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	best := map[string]int{}
	for rows.Next() {
		var id string
		var stars int
		if err := rows.Scan(&id, &stars); err != nil {
			return nil, err
		}
		best[id] = stars
	}
	return best, rows.Err()
}

func (s *Service) expireDueTx(ctx context.Context, tx *sql.Tx, nowMS int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='expired',terminal_at_ms=?,terminal_reason='prepare_expired',revision=revision+1
 WHERE state='prepared' AND prepare_until_ms<=?`, nowMS, nowMS)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='expired',terminal_at_ms=?,terminal_reason='submit_expired',revision=revision+1
 WHERE state='active' AND submit_until_ms<?`, nowMS, nowMS)
	return err
}

// Admission may reclaim a bounded batch of expired terminal summaries before
// treating the summary counter as full. The delete trigger updates capacity in
// this transaction, and the state predicate cannot remove an in-flight game.
func (s *Service) challengeCapacityTx(ctx context.Context, tx *sql.Tx, nowMS int64) error {
	var active, summaries int
	if err := tx.QueryRowContext(ctx, `SELECT active_challenges,summary_rows FROM fatfish_capacity WHERE id=1`).Scan(&active, &summaries); err != nil {
		return err
	}
	if summaries >= 1000000 {
		cutoff := nowMS - int64(summaryLifetime/time.Millisecond)
		if cutoff >= 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM fatfish_challenges WHERE id IN (
			 SELECT id FROM fatfish_challenges WHERE terminal_at_ms<?
			 AND state IN ('settled_pass','settled_fail','abandoned','expired','cancelled_refunded')
			 ORDER BY terminal_at_ms,id LIMIT 100)`, cutoff); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT active_challenges,summary_rows FROM fatfish_capacity WHERE id=1`).Scan(&active, &summaries); err != nil {
				return err
			}
		}
	}
	if active >= 10000 || summaries >= 1000000 {
		return ErrCapacity
	}
	return nil
}

func (s *Service) Prepare(ctx context.Context, userID int64, input PrepareInput, key string) (ChallengeView, error) {
	if !db.ValidateOpaqueID(input.PeriodID, "ffp_") || !db.ValidateOpaqueID(input.NodeID, "ffn_") {
		return ChallengeView{}, ErrInvalid
	}
	revision, err := parseRevision(input.ExpectedRevision)
	if err != nil {
		return ChallengeView{}, err
	}
	hash, err := hex.DecodeString(input.TabCapabilityHash)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != input.TabCapabilityHash {
		return ChallengeView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return ChallengeView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "user", userID, key, http.MethodPost, "/challenges/prepare", nil, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if replay, ok, replayErr := replayMutation[ChallengeView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if err = s.admissionTx(ctx, tx, userID, nowMS); err != nil {
		return ChallengeView{}, err
	}
	if err = s.expireDueTx(ctx, tx, nowMS); err != nil {
		return ChallengeView{}, err
	}
	n, err := readNodeSnapshotTx(ctx, tx, input.PeriodID, input.NodeID)
	if err != nil {
		return ChallengeView{}, err
	}
	if n.revision != revision {
		return ChallengeView{}, ErrConflict
	}
	if !availablePeriod(n.periodState, n.periodStart, n.periodEnd, nowMS) || n.periodPaused {
		return ChallengeView{}, ErrClosed
	}
	var unlocked int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_progress WHERE user_id=? AND period_id=? AND node_id=?`, userID, input.PeriodID, input.NodeID).Scan(&unlocked)
	if errors.Is(err, sql.ErrNoRows) {
		return ChallengeView{}, ErrForbidden
	}
	if err != nil {
		return ChallengeView{}, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_challenges WHERE user_id=? AND playtest=0 AND state IN ('prepared','active','verifying')`, userID).Scan(&active); err != nil {
		return ChallengeView{}, err
	}
	if active != 0 {
		return ChallengeView{}, ErrConflict
	}
	if err = s.challengeCapacityTx(ctx, tx, nowMS); err != nil {
		return ChallengeView{}, err
	}
	id, err := db.GenerateOpaqueID("ffc_")
	if err != nil {
		return ChallengeView{}, err
	}
	var seed [32]byte
	if _, err = io.ReadFull(s.random, seed[:]); err != nil {
		return ChallengeView{}, err
	}
	contentHash := hex.EncodeToString(n.contentHash)
	commit, err := engine.SeedCommitForVersion(id, n.periodID, n.nodeID, contentHash, n.engineVersion, n.scoringVersion, seed)
	if err != nil {
		return ChallengeView{}, err
	}
	commitRaw, _ := hex.DecodeString(commit)
	_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_challenges
 (id,user_id,playtest,period_id,node_id,node_revision,version_id,tab_capability_hash,seed,seed_commit,
 state,prepared_at_ms,prepare_until_ms,ticket_price_mag,revision)
 VALUES(?,?,0,?,?,?,?,?,?,?,'prepared',?,?,?,1)`, id, userID, n.periodID, n.nodeID, n.revision, n.versionID, hash, seed[:], commitRaw, nowMS, nowMS+60000, n.price)
	if err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	view, err := c.view(nowMS, "")
	if err != nil {
		return ChallengeView{}, err
	}
	view.NodeRevision = strconv.FormatInt(revision, 10)
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	return view, tx.Commit()
}
