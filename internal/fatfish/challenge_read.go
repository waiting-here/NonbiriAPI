package fatfish

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type challengeRow struct {
	id, periodID, nodeID, versionID, state, reason                    string
	userID                                                            int64
	playtest                                                          bool
	nodeRevision                                                      int64
	capabilityHash, seed, seedCommit, price, contentHash, inputDigest []byte
	prepared, prepareUntil                                            int64
	start, end, submitUntil, received, terminal                       sql.NullInt64
	resultJSON, levelJSON                                             sql.NullString
	engineVersion, scoringVersion, durationSeconds                    int
	maxStars                                                          int
	revision                                                          int64
}

const challengeSelect = `SELECT c.id,c.user_id,c.playtest,COALESCE(c.period_id,''),COALESCE(c.node_id,''),
 COALESCE(c.node_revision,0),c.version_id,c.tab_capability_hash,c.seed,c.seed_commit,c.state,
 c.prepared_at_ms,c.prepare_until_ms,c.start_at_ms,c.end_at_ms,c.submit_until_ms,
 c.ticket_price_mag,c.input_digest,c.received_at_ms,c.verified_result_json,c.terminal_at_ms,
 c.terminal_reason,c.revision,v.content_hash,v.content_json,v.engine_version,v.scoring_version,
 v.duration_seconds,v.maximum_stars
 FROM fatfish_challenges c JOIN fatfish_level_versions v ON v.id=c.version_id`

func scanChallenge(row *sql.Row) (challengeRow, error) {
	var c challengeRow
	var playtest int
	err := row.Scan(&c.id, &c.userID, &playtest, &c.periodID, &c.nodeID, &c.nodeRevision, &c.versionID,
		&c.capabilityHash, &c.seed, &c.seedCommit, &c.state, &c.prepared, &c.prepareUntil,
		&c.start, &c.end, &c.submitUntil, &c.price, &c.inputDigest, &c.received, &c.resultJSON,
		&c.terminal, &c.reason, &c.revision, &c.contentHash, &c.levelJSON,
		&c.engineVersion, &c.scoringVersion, &c.durationSeconds, &c.maxStars)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.playtest = playtest == 1
	if len(c.capabilityHash) != 32 || len(c.seed) != 32 || len(c.seedCommit) != 32 || len(c.contentHash) != 32 || len(c.price) != 16 {
		return c, ErrInvariant
	}
	return c, nil
}

func readChallengeTx(ctx context.Context, tx *sql.Tx, id string) (challengeRow, error) {
	if !db.ValidateOpaqueID(id, "ffc_") {
		return challengeRow{}, ErrInvalid
	}
	return scanChallenge(tx.QueryRowContext(ctx, challengeSelect+` WHERE c.id=?`, id))
}

func readCurrentChallengeTx(ctx context.Context, tx *sql.Tx, userID int64) (challengeRow, error) {
	return scanChallenge(tx.QueryRowContext(ctx, challengeSelect+` WHERE c.user_id=? AND c.playtest=0 AND c.state IN ('prepared','active','verifying')`, userID))
}

func (c challengeRow) view(nowMS int64, capability string) (ChallengeView, error) {
	price, err := amountText(c.price)
	if err != nil {
		return ChallengeView{}, err
	}
	v := ChallengeView{ID: c.id, State: c.state, PeriodID: c.periodID, NodeID: c.nodeID,
		VersionID: c.versionID, NodeRevision: strconv.FormatInt(c.nodeRevision, 10),
		ContentHash: hex.EncodeToString(c.contentHash), EngineVersion: c.engineVersion,
		ScoringVersion: c.scoringVersion, SeedCommit: hex.EncodeToString(c.seedCommit),
		PreparedAtMS: c.prepared, PrepareUntilMS: c.prepareUntil, ServerNowMS: nowMS,
		TicketPrice: price}
	if c.nodeRevision == 0 {
		v.NodeRevision = ""
	}
	if c.start.Valid {
		value := c.start.Int64
		v.StartAtMS = &value
	}
	if c.end.Valid {
		value := c.end.Int64
		v.EndAtMS = &value
	}
	if c.submitUntil.Valid {
		value := c.submitUntil.Int64
		v.SubmitUntilMS = &value
	}
	if c.resultJSON.Valid && (c.state == "settled_pass" || c.state == "settled_fail") {
		var result ResultView
		if err := json.Unmarshal([]byte(c.resultJSON.String), &result); err != nil {
			return ChallengeView{}, ErrInvariant
		}
		result.State = c.state
		v.Result = &result
	} else if c.state == "cancelled_refunded" || c.state == "abandoned" || c.state == "expired" {
		charge := "0"
		if c.start.Valid {
			charge = price
		}
		refund := "0"
		if c.state == "cancelled_refunded" {
			refund = charge
		}
		v.Result = &ResultView{State: c.state, Reason: c.reason, ScoreUnits: "0", Rewards: "0", TicketCharge: charge, TicketRefund: refund,
			EngineVersion: c.engineVersion, ScoringVersion: c.scoringVersion, ContentHash: v.ContentHash, SeedCommit: v.SeedCommit}
	}
	// The complete level and seed are recovery material; neither is projected to
	// another page merely because that page has the user's cookie.
	if capability != "" && sameCapability(c.capabilityHash, capability) {
		v.Level = json.RawMessage(c.levelJSON.String)
		if c.start.Valid {
			v.Seed = hex.EncodeToString(c.seed)
		}
	}
	return v, nil
}

func (s *Service) Challenge(ctx context.Context, userID int64, id, capability string) (ChallengeView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != userID || c.playtest {
		return ChallengeView{}, ErrNotFound
	}
	v, err := c.view(nowMS, capability)
	if err != nil {
		return ChallengeView{}, err
	}
	return v, tx.Commit()
}

func (s *Service) CurrentChallenge(ctx context.Context, userID int64, capability string) (*ChallengeView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return nil, err
	}
	c, err := readCurrentChallengeTx(ctx, tx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	v, err := c.view(nowMS, capability)
	if err != nil {
		return nil, err
	}
	return &v, tx.Commit()
}
