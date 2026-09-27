package fatfish

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type UnlockInput struct {
	ExpectedRevision string `json:"expected_revision"`
}

func readProgressTx(ctx context.Context, tx *sql.Tx, userID int64, periodID, nodeID string) (Progress, error) {
	var value Progress
	var passed int
	var score int64
	var bestAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT passed,best_stars,best_score_units,best_at_ms FROM fatfish_progress
 WHERE user_id=? AND period_id=? AND node_id=?`, userID, periodID, nodeID).Scan(&passed, &value.BestStars, &score, &bestAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{BestScoreUnits: "0"}, nil
	}
	if err != nil {
		return value, err
	}
	value.Unlocked = true
	value.Passed = passed == 1
	value.BestScoreUnits = strconv.FormatInt(score, 10)
	if bestAt.Valid {
		at := bestAt.Int64
		value.BestAtMS = &at
	}
	return value, nil
}

func (s *Service) Unlock(ctx context.Context, userID int64, periodID, nodeID string, input UnlockInput, key string) (Progress, error) {
	if !db.ValidateOpaqueID(periodID, "ffp_") || !db.ValidateOpaqueID(nodeID, "ffn_") {
		return Progress{}, ErrInvalid
	}
	revision, err := parseRevision(input.ExpectedRevision)
	if err != nil {
		return Progress{}, err
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return Progress{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Progress{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return Progress{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "user", userID, key, http.MethodPost, "/periods/{p}/nodes/{n}/unlock", []string{periodID, nodeID}, input, nowMS)
	if err != nil {
		return Progress{}, err
	}
	if replay, ok, replayErr := replayMutation[Progress](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	value, err := readProgressTx(ctx, tx, userID, periodID, nodeID)
	if err != nil {
		return Progress{}, err
	}
	if !value.Unlocked {
		if err = s.admissionTx(ctx, tx, userID, nowMS); err != nil {
			return Progress{}, err
		}
		n, err := readNodeSnapshotTx(ctx, tx, periodID, nodeID)
		if err != nil {
			return Progress{}, err
		}
		if n.revision != revision {
			return Progress{}, ErrConflict
		}
		if !availablePeriod(n.periodState, n.periodStart, n.periodEnd, nowMS) || n.periodPaused {
			return Progress{}, ErrClosed
		}
		best, err := bestStarsTx(ctx, tx, userID, periodID)
		if err != nil {
			return Progress{}, err
		}
		if !n.condition.Eligible(best) {
			return Progress{}, ErrForbidden
		}
		op, err := s.chargeTx(ctx, tx, userID, nowMS, "unlock", unlockReceiptKey(userID, periodID, nodeID), n.unlockCost)
		if err != nil {
			return Progress{}, err
		}
		if !op.Valid {
			return Progress{}, ErrInvariant
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_progress(user_id,period_id,node_id,unlock_operation_id,unlocked_at,
 passed,best_stars,best_score_units) VALUES(?,?,?,?,?,0,0,0)`, userID, periodID, nodeID, op.String, nowMS/1000)
		if err != nil {
			return Progress{}, err
		}
		value = Progress{Unlocked: true, BestScoreUnits: "0"}
	}
	if err = completeMutationTx(ctx, tx, decision, value, http.StatusOK); err != nil {
		return Progress{}, err
	}
	return value, tx.Commit()
}

const collectionPageSize = 20

func validCollectionPage(page int) bool { return page >= 1 && page <= 1000000 }

type periodScanner interface{ Scan(...any) error }

func scanPeriodSummary(row periodScanner, nowMS int64) (PeriodView, error) {
	var p PeriodView
	var visible, paused, past, active int
	var revision int64
	err := row.Scan(&p.ID, &p.Title, &p.Description, &p.State, &visible, &paused, &past, &p.StartsAt, &p.EndsAt, &revision, &active)
	if err != nil {
		return p, err
	}
	p.Visible, p.Paused, p.PastPublic = visible == 1, paused == 1, past == 1
	p.Revision = strconv.FormatInt(revision, 10)
	p.LeaderboardFinal = active == 0 && nowMS >= p.EndsAt*1000
	return p, nil
}

const periodSummaryQuery = `SELECT p.id,p.title,p.description,p.state,p.visible,p.paused,p.past_public,p.starts_at,p.ends_at,p.revision,
 (SELECT count(*) FROM fatfish_challenges c WHERE c.period_id=p.id AND c.state IN ('prepared','active','verifying')) FROM fatfish_periods p`

func (s *Service) ListPeriods(ctx context.Context, userID int64, page int) (PeriodPage, error) {
	if !validCollectionPage(page) {
		return PeriodPage{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return PeriodPage{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PeriodPage{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return PeriodPage{}, err
	}
	rows, err := tx.QueryContext(ctx, periodSummaryQuery+` WHERE p.visible=1 AND (p.state='open' AND (p.ends_at*1000>? OR p.past_public=1) OR p.state='closed' AND p.past_public=1) ORDER BY p.starts_at DESC,p.id LIMIT ? OFFSET ?`, nowMS, collectionPageSize+1, (page-1)*collectionPageSize)
	if err != nil {
		return PeriodPage{}, err
	}
	out := PeriodPage{Items: []PeriodView{}, Page: page, PageSize: collectionPageSize}
	for rows.Next() {
		item, scanErr := scanPeriodSummary(rows, nowMS)
		if scanErr != nil {
			rows.Close()
			return PeriodPage{}, scanErr
		}
		if len(out.Items) == collectionPageSize {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return PeriodPage{}, err
	}
	rows.Close()
	return out, tx.Commit()
}

func (s *Service) Period(ctx context.Context, userID int64, id string) (PeriodView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return PeriodView{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PeriodView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return PeriodView{}, err
	}
	value, err := s.periodTx(ctx, tx, userID, id, nowMS)
	if err != nil {
		return PeriodView{}, err
	}
	return value, tx.Commit()
}

func (s *Service) periodTx(ctx context.Context, tx *sql.Tx, userID int64, id string, nowMS int64) (PeriodView, error) {
	if !db.ValidateOpaqueID(id, "ffp_") {
		return PeriodView{}, ErrInvalid
	}
	var p PeriodView
	var visible, paused, past int
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT id,title,description,state,visible,paused,past_public,starts_at,ends_at,revision
 FROM fatfish_periods WHERE id=?`, id).Scan(&p.ID, &p.Title, &p.Description, &p.State, &visible, &paused, &past, &p.StartsAt, &p.EndsAt, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Visible = visible == 1
	p.Paused = paused == 1
	p.PastPublic = past == 1
	p.Revision = strconv.FormatInt(revision, 10)
	if p.State == "draft" || (p.State == "closed" || nowMS >= p.EndsAt*1000) && !p.PastPublic {
		return PeriodView{}, ErrNotFound
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_challenges WHERE period_id=? AND state IN ('prepared','active','verifying')`, id).Scan(&active); err != nil {
		return p, err
	}
	p.LeaderboardFinal = active == 0 && nowMS >= p.EndsAt*1000
	best, err := bestStarsTx(ctx, tx, userID, id)
	if err != nil {
		return p, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.title,n.description,n.map_x,n.map_y,n.ord,n.current_revision,
 r.version_id,r.condition_json,r.hidden_until_eligible,r.unlock_cost_mag,r.ticket_price_mag,
	 r.first_clear_reward_mag,r.star1_reward_mag,r.star2_reward_mag,r.star3_reward_mag,v.content_hash
 FROM fatfish_nodes n JOIN fatfish_node_revisions r ON r.node_id=n.id AND r.revision=n.current_revision
 JOIN fatfish_level_versions v ON v.id=r.version_id WHERE n.period_id=? ORDER BY n.ord,n.id`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Nodes = []NodeView{}
	for rows.Next() {
		var n NodeView
		var revision int64
		var hidden int
		var conditionJSON string
		var mags [6][]byte
		var hash []byte
		if err = rows.Scan(&n.ID, &n.Title, &n.Description, &n.MapX, &n.MapY, &n.Order, &revision,
			&n.VersionID, &conditionJSON, &hidden, &mags[0], &mags[1], &mags[2], &mags[3], &mags[4], &mags[5], &hash); err != nil {
			return p, err
		}
		n.PeriodID = id
		n.Revision = strconv.FormatInt(revision, 10)
		n.Hidden = hidden == 1
		condition, err := ParseCondition([]byte(conditionJSON))
		if err != nil {
			return p, ErrInvariant
		}
		n.Eligible = condition.Eligible(best)
		n.Progress, err = readProgressTx(ctx, tx, userID, id, n.ID)
		if err != nil {
			return p, err
		}
		if n.Hidden && !n.Eligible && !n.Progress.Unlocked {
			n.Title = ""
			n.Description = ""
			n.VersionID = ""
			n.Revision = ""
		} else {
			n.ContentHash = hex.EncodeToString(hash)
			n.Amounts = &Amounts{}
			for i, raw := range mags {
				text, err := amountText(raw)
				if err != nil {
					return p, err
				}
				switch i {
				case 0:
					n.Amounts.UnlockCost = text
				case 1:
					n.Amounts.TicketPrice = text
				case 2:
					n.Amounts.FirstClearReward = text
				case 3:
					n.Amounts.StarRewards[0] = text
				case 4:
					n.Amounts.StarRewards[1] = text
				case 5:
					n.Amounts.StarRewards[2] = text
				}
			}
		}
		p.Nodes = append(p.Nodes, n)
	}
	return p, rows.Err()
}

func (s *Service) Node(ctx context.Context, userID int64, periodID, nodeID string) (NodeView, error) {
	if !db.ValidateOpaqueID(periodID, "ffp_") || !db.ValidateOpaqueID(nodeID, "ffn_") {
		return NodeView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return NodeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return NodeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeUserTx(ctx, tx, userID, nowMS, false); err != nil {
		return NodeView{}, err
	}
	p, err := s.periodTx(ctx, tx, userID, periodID, nowMS)
	if err != nil {
		return NodeView{}, err
	}
	visible := make(map[string]bool, len(p.Nodes))
	for _, n := range p.Nodes {
		visible[n.ID] = !n.Hidden || n.Eligible || n.Progress.Unlocked
	}
	for _, n := range p.Nodes {
		if n.ID == nodeID {
			if n.Hidden && !visible[n.ID] {
				return n, tx.Commit()
			}
			var conditionJSON, levelJSON string
			err = tx.QueryRowContext(ctx, `SELECT r.condition_json,v.content_json FROM fatfish_nodes x
 JOIN fatfish_node_revisions r ON r.node_id=x.id AND r.revision=x.current_revision
 JOIN fatfish_level_versions v ON v.id=r.version_id WHERE x.period_id=? AND x.id=?`, periodID, nodeID).Scan(&conditionJSON, &levelJSON)
			if err != nil {
				return NodeView{}, err
			}
			condition, parseErr := ParseCondition([]byte(conditionJSON))
			if parseErr != nil {
				return NodeView{}, ErrInvariant
			}
			best, readErr := bestStarsTx(ctx, tx, userID, periodID)
			if readErr != nil {
				return NodeView{}, readErr
			}
			hint := condition.Project(best, visible)
			n.ConditionHint = &hint
			n.Level = json.RawMessage(levelJSON)
			return n, tx.Commit()
		}
	}
	return NodeView{}, ErrNotFound
}
