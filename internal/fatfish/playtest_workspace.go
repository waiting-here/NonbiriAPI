package fatfish

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type PlaytestAbandonInput struct {
	ExpectedRevision string `json:"expected_revision"`
}

// AbandonPlaytest ends only this administrator's unfinished no-charge playtest.
// It requires no lost tab capability and never grants a pass or a payment.
func (s *Service) AbandonPlaytest(ctx context.Context, actorID int64, id string, input PlaytestAbandonInput, key string) (ChallengeView, error) {
	if !db.ValidateOpaqueID(id, "ffc_") {
		return ChallengeView{}, ErrInvalid
	}
	expected, err := parseRevision(input.ExpectedRevision)
	if err != nil {
		return ChallengeView{}, err
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
	c, err := readChallengeTx(ctx, tx, id)
	if err != nil {
		return ChallengeView{}, err
	}
	if c.userID != actorID || !c.playtest {
		return ChallengeView{}, ErrNotFound
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPost, "/playtests/{id}/abandon", []string{id}, input, nowMS)
	if err != nil {
		return ChallengeView{}, err
	}
	if _, ok, replayErr := replayMutation[ChallengeView](decision); replayErr != nil {
		return ChallengeView{}, replayErr
	} else if ok {
		view, err := c.view(nowMS, "")
		if err != nil {
			return ChallengeView{}, err
		}
		return view, tx.Commit()
	}
	if c.state == "prepared" || c.state == "active" || c.state == "verifying" {
		if c.revision != expected {
			return ChallengeView{}, ErrConflict
		}
		// Once a replay fact has won, settlement owns the terminal outcome.
		if c.state == "verifying" && c.resultJSON.Valid {
			return ChallengeView{}, ErrConflict
		}
		result, err := tx.ExecContext(ctx, "UPDATE fatfish_challenges SET state='abandoned',terminal_at_ms=?,terminal_reason='admin_playtest_abandoned',revision=revision+1 WHERE id=? AND state=? AND revision=?", nowMS, id, c.state, c.revision)
		if err != nil {
			return ChallengeView{}, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return ChallengeView{}, err
		}
		if changed != 1 {
			return ChallengeView{}, ErrConflict
		}
		c, err = readChallengeTx(ctx, tx, id)
		if err != nil {
			return ChallengeView{}, err
		}
	}
	view, err := c.view(nowMS, "")
	if err != nil {
		return ChallengeView{}, err
	}
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return ChallengeView{}, err
	}
	if err = tx.Commit(); err != nil {
		return ChallengeView{}, err
	}
	s.cancelJob(id)
	return view, nil
}

type CurrentPlaytestView struct {
	VersionNumber string `json:"version_number"`
	ID            string `json:"id"`
	Revision      string `json:"revision"`
	State         string `json:"state"`
	VersionID     string `json:"version_id"`
	LevelTitle    string `json:"level_title"`
	ContentHash   string `json:"content_hash"`
	EngineVersion int    `json:"engine_version"`
	ExpiresAtMS   int64  `json:"expires_at_ms"`
	ServerNowMS   int64  `json:"server_now_ms"`
}

func (s *Service) CurrentPlaytest(ctx context.Context, actorID int64) (*CurrentPlaytestView, error) {
	nowMS, err := s.nowMS()
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return nil, err
	}
	c, err := scanChallenge(tx.QueryRowContext(ctx, challengeSelect+" WHERE c.user_id=? AND c.playtest=1 AND c.state IN ('prepared','active','verifying')", actorID))
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	safe, err := c.view(nowMS, "")
	if err != nil {
		return nil, err
	}
	expires := c.prepareUntil
	if c.submitUntil.Valid {
		expires = c.submitUntil.Int64
	}
	view := &CurrentPlaytestView{ID: c.id, Revision: strconv.FormatInt(c.revision, 10), State: c.state, VersionID: c.versionID, ContentHash: safe.ContentHash, EngineVersion: c.engineVersion, ExpiresAtMS: expires, ServerNowMS: nowMS}
	if err = tx.QueryRowContext(ctx, "SELECT l.title,CAST(v.version_number AS TEXT) FROM fatfish_level_versions v JOIN fatfish_levels l ON l.id=v.level_id WHERE v.id=?", c.versionID).Scan(&view.LevelTitle, &view.VersionNumber); err != nil {
		return nil, err
	}
	return view, tx.Commit()
}
