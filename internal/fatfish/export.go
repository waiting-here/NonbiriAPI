package fatfish

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func (a *LifecycleAdapter) ExportFatFish(ctx context.Context, tx *sql.Tx, request lifecycle.ExportRequest) (lifecycle.FatFishExport, lifecycle.ExportFinalizer, error) {
	if a == nil || a.service == nil || tx == nil || request.UserID <= 0 || request.DecisionNow < 0 || request.Limit < 1 || request.Limit > lifecycle.CollectionLimit {
		return lifecycle.FatFishExport{}, nil, ErrInvalid
	}
	out := lifecycle.FatFishExport{Summaries: []lifecycle.FatFishSummaryExport{}, Progress: []lifecycle.FatFishProgressExport{}}
	cutoff := request.DecisionNow - int64(summaryLifetime.Seconds())
	if cutoff < 0 {
		cutoff = 0
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,COALESCE(c.period_id,''),COALESCE(c.node_id,''),c.version_id,
 v.engine_version,v.scoring_version,c.state,c.prepared_at_ms,c.start_at_ms,c.terminal_at_ms,
 c.ticket_price_mag,c.seed_commit,c.verified_result_json FROM fatfish_challenges c
 JOIN fatfish_level_versions v ON v.id=c.version_id WHERE c.user_id=? AND c.playtest=0
 AND c.terminal_at_ms IS NOT NULL AND c.terminal_at_ms>=? ORDER BY c.terminal_at_ms DESC,c.id LIMIT ?`, request.UserID, cutoff*1000, request.Limit+1)
	if err != nil {
		return out, nil, err
	}
	for rows.Next() {
		var item lifecycle.FatFishSummaryExport
		var started sql.NullInt64
		var price, commit []byte
		var resultJSON sql.NullString
		if err = rows.Scan(&item.ID, &item.PeriodID, &item.NodeID, &item.VersionID, &item.EngineVersion,
			&item.ScoringVersion, &item.State, &item.PreparedAt, &started, &item.CompletedAt, &price, &commit, &resultJSON); err != nil {
			rows.Close()
			return out, nil, err
		}
		if len(out.Summaries) >= request.Limit {
			rows.Close()
			return out, nil, lifecycle.ErrTooLarge
		}
		if started.Valid {
			value := started.Int64
			item.StartedAt = &value
		}
		item.SeedCommit = hex.EncodeToString(commit)
		item.ScoreUnits = "0"
		item.TicketCharge = "0"
		item.TicketRefund = "0"
		item.Rewards = "0"
		if started.Valid {
			item.TicketCharge, err = amountText(price)
			if err != nil {
				rows.Close()
				return out, nil, err
			}
		}
		if item.State == "cancelled_refunded" {
			item.TicketRefund = item.TicketCharge
		}
		if resultJSON.Valid && (item.State == "settled_pass" || item.State == "settled_fail") {
			var result ResultView
			if err = json.Unmarshal([]byte(resultJSON.String), &result); err != nil {
				rows.Close()
				return out, nil, ErrInvariant
			}
			item.Passed = result.Passed
			item.Stars = result.Stars
			item.ScoreUnits = result.ScoreUnits
			item.Rewards = result.Rewards
			item.CommitmentVerified = result.CommitmentVerified
		}
		out.Summaries = append(out.Summaries, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, nil, err
	}
	rows.Close()
	remaining := request.Limit - len(out.Summaries)
	rows, err = tx.QueryContext(ctx, `SELECT period_id,node_id,unlocked_at,unlock_operation_id,passed,best_stars,
 best_score_units,best_at_ms,best_version_id FROM fatfish_progress WHERE user_id=? ORDER BY period_id,node_id LIMIT ?`, request.UserID, remaining+1)
	if err != nil {
		return out, nil, err
	}
	for rows.Next() {
		var item lifecycle.FatFishProgressExport
		var op, version sql.NullString
		var bestAt sql.NullInt64
		var passed int
		var score int64
		if err = rows.Scan(&item.PeriodID, &item.NodeID, &item.UnlockedAt, &op, &passed, &item.BestStars, &score, &bestAt, &version); err != nil {
			rows.Close()
			return out, nil, err
		}
		if len(out.Progress) >= remaining {
			rows.Close()
			return out, nil, lifecycle.ErrTooLarge
		}
		if op.Valid {
			value := op.String
			item.UnlockOperationID = &value
		}
		if bestAt.Valid {
			value := bestAt.Int64
			item.BestAt = &value
		}
		if version.Valid {
			value := version.String
			item.BestVersionID = &value
		}
		item.Passed = passed == 1
		item.BestScoreUnits = strconv.FormatInt(score, 10)
		out.Progress = append(out.Progress, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, nil, err
	}
	rows.Close()
	return out, nil, nil
}
