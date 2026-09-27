package fatfish

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
)

type PlaytestInput struct {
	VersionID         string `json:"version_id"`
	TabCapabilityHash string `json:"tab_capability_hash"`
}
type PlaytestView struct {
	ID         string     `json:"id"`
	VersionID  string     `json:"version_id"`
	Passed     bool       `json:"passed"`
	Stars      int        `json:"stars"`
	ScoreUnits string     `json:"score_units"`
	DurationMS int64      `json:"duration_ms"`
	Result     ResultView `json:"result"`
	CreatedAt  int64      `json:"created_at"`
}

func (s *Service) PreparePlaytest(ctx context.Context, actorID int64, input PlaytestInput, key string) (ChallengeView, error) {
	if !db.ValidateOpaqueID(input.VersionID, "ffv_") {
		return ChallengeView{}, ErrInvalid
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
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return ChallengeView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPost, "/playtests", nil, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if replay, ok, replayErr := replayMutation[ChallengeView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if err = s.expireDueTx(ctx, tx, nowMS); err != nil {
		return ChallengeView{}, err
	}
	version, err := readVersionTx(ctx, tx, input.VersionID)
	if err != nil {
		return ChallengeView{}, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM fatfish_challenges WHERE user_id=? AND state IN ('prepared','active','verifying')`, actorID).Scan(&active); err != nil {
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
	commit, err := engine.SeedCommit(id, input.VersionID, input.VersionID, version.ContentHash, seed)
	if err != nil {
		return ChallengeView{}, err
	}
	commitRaw, _ := hex.DecodeString(commit)
	_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_challenges(id,user_id,playtest,period_id,node_id,node_revision,version_id,tab_capability_hash,seed,seed_commit,state,prepared_at_ms,prepare_until_ms,ticket_price_mag,revision)
 VALUES(?,?,1,NULL,NULL,NULL,?,?,?,?,'prepared',?,?,zeroblob(16),1)`, id, actorID, input.VersionID, hash, seed[:], commitRaw, nowMS, nowMS+60000)
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
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	return view, tx.Commit()
}

func (s *Service) StartPlaytest(ctx context.Context, actorID int64, id string, input StartInput, key string) (ChallengeView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != actorID || !c.playtest {
		return ChallengeView{}, ErrNotFound
	}
	if !sameCapability(c.capabilityHash, input.TabCapability) {
		return ChallengeView{}, ErrForbidden
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPost, "/playtests/{id}/start", []string{id}, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if _, ok, replayErr := replayMutation[ChallengeView](decision); replayErr != nil {
		return ChallengeView{}, replayErr
	} else if ok {
		view, err := c.view(nowMS, input.TabCapability)
		if err != nil {
			return ChallengeView{}, err
		}
		return view, tx.Commit()
	}
	if c.state == "prepared" {
		if nowMS >= c.prepareUntil {
			return ChallengeView{}, ErrClosed
		}
		start := nowMS + 3000
		end := start + int64(c.durationSeconds)*1000
		res, err := tx.ExecContext(ctx, `UPDATE fatfish_challenges SET state='active',start_at_ms=?,end_at_ms=?,submit_until_ms=?,revision=revision+1 WHERE id=? AND state='prepared' AND revision=?`, start, end, end+1800000, id, c.revision)
		if err != nil {
			return ChallengeView{}, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return ChallengeView{}, err
		}
		if affected != 1 {
			return ChallengeView{}, ErrConflict
		}
		c, err = readChallengeTx(ctx, tx, id)
		if err != nil {
			return ChallengeView{}, err
		}
	} else if c.state != "active" && c.state != "verifying" && c.state != "settled_pass" && c.state != "settled_fail" {
		return ChallengeView{}, ErrClosed
	}
	view, err := c.view(nowMS, input.TabCapability)
	if err != nil {
		return ChallengeView{}, err
	}
	receipt := view
	receipt.Level = nil
	receipt.Seed = ""
	if err = completeMutationTx(ctx, tx, decision, receipt, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	return view, tx.Commit()
}

func (s *Service) PlaytestChallenge(ctx context.Context, actorID int64, id, capability string) (ChallengeView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return ChallengeView{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ChallengeView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return ChallengeView{}, err
	}
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != actorID || !c.playtest {
		return ChallengeView{}, ErrNotFound
	}
	v, err := c.view(nowMS, capability)
	if err != nil {
		return ChallengeView{}, err
	}
	return v, tx.Commit()
}

func (s *Service) Playtests(ctx context.Context, actorID int64, versionID string) ([]PlaytestView, error) {
	if !db.ValidateOpaqueID(versionID, "ffv_") {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,version_id,passed,stars,score_units,duration_ms,result_json,created_at FROM fatfish_playtests WHERE version_id=? ORDER BY created_at DESC,id LIMIT 100`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlaytestView{}
	for rows.Next() {
		var p PlaytestView
		var passed int
		var score int64
		var raw string
		if err = rows.Scan(&p.ID, &p.VersionID, &passed, &p.Stars, &score, &p.DurationMS, &raw, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Passed = passed == 1
		p.ScoreUnits = strconv.FormatInt(score, 10)
		if err = json.Unmarshal([]byte(raw), &p.Result); err != nil {
			return nil, ErrInvariant
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
