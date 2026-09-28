package fatfish

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type LevelDeletionView struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	DeletedAt int64  `json:"deleted_at"`
}

// DeleteLevel removes the editable source from the library without changing
// immutable versions referenced by activity nodes or accepted challenges.
func (s *Service) DeleteLevel(ctx context.Context, actorID int64, id, expectedRevision, key string) (LevelDeletionView, error) {
	if !db.ValidateOpaqueID(id, "ffl_") {
		return LevelDeletionView{}, ErrInvalid
	}
	expected, err := parseRevision(expectedRevision)
	if err != nil || expected == math.MaxInt64 {
		return LevelDeletionView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return LevelDeletionView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LevelDeletionView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return LevelDeletionView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodDelete, "/levels/{id}", []string{id}, struct{ ExpectedRevision string }{expectedRevision}, nowMS)
	if err != nil {
		return LevelDeletionView{}, err
	}
	if replay, ok, replayErr := replayMutation[LevelDeletionView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if _, err = readLevelTx(ctx, tx, id); err != nil {
		return LevelDeletionView{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE fatfish_levels SET revision=revision+1,actor_user_id=?,updated_at=?
 WHERE id=? AND revision=? AND NOT EXISTS(SELECT 1 FROM fatfish_deleted_levels WHERE level_id=fatfish_levels.id)`, actorID, nowMS/1000, id, expected)
	if err != nil {
		return LevelDeletionView{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return LevelDeletionView{}, err
		}
		return LevelDeletionView{}, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO fatfish_deleted_levels(level_id,deleted_at) VALUES(?,?)`, id, nowMS/1000); err != nil {
		return LevelDeletionView{}, err
	}
	view := LevelDeletionView{ID: id, Revision: strconv.FormatInt(expected+1, 10), DeletedAt: nowMS / 1000}
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return LevelDeletionView{}, err
	}
	return view, tx.Commit()
}

func availableVersionTx(ctx context.Context, tx *sql.Tx, id string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM fatfish_level_versions v WHERE v.id=?
 AND NOT EXISTS(SELECT 1 FROM fatfish_deleted_levels d WHERE d.level_id=v.level_id)`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
