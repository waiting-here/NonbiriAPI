package fatfish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/fatfish/engine"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type LevelInput struct {
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Draft            json.RawMessage `json:"draft"`
	ExpectedRevision string          `json:"expected_revision,omitempty"`
}
type LevelView struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Draft       json.RawMessage `json:"draft,omitempty"`
	Revision    string          `json:"revision"`
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}
type VersionView struct {
	ID              string          `json:"id"`
	LevelID         string          `json:"level_id"`
	ContentHash     string          `json:"content_hash"`
	EngineVersion   int             `json:"engine_version"`
	ScoringVersion  int             `json:"scoring_version"`
	DurationSeconds int             `json:"duration_seconds"`
	MaximumStars    int             `json:"maximum_stars"`
	Content         json.RawMessage `json:"content,omitempty"`
	CreatedAt       int64           `json:"created_at"`
}

type LevelPage struct {
	Items    []LevelView `json:"items"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	HasMore  bool        `json:"has_more"`
}

type VersionPage struct {
	Items    []VersionView `json:"items"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	HasMore  bool          `json:"has_more"`
}

func validText(value string, maxBytes int, nonempty bool) bool {
	return utf8.ValidString(value) && len([]byte(value)) <= maxBytes && (!nonempty || len(value) > 0)
}

func (s *Service) SaveLevel(ctx context.Context, actorID int64, id string, input LevelInput, key string) (LevelView, error) {
	if !validText(input.Title, 128, true) || !validText(input.Description, 4096, false) || len(input.Draft) == 0 || len(input.Draft) > engine.MaxLevelBytes || strictjson.ValidateObjectWithFieldLimit(input.Draft, 16384) != nil {
		return LevelView{}, ErrInvalid
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, input.Draft); err != nil || compact.Len() > 262144 {
		return LevelView{}, ErrInvalid
	}
	input.Draft = append(json.RawMessage(nil), compact.Bytes()...)
	if _, err := engine.ParseLevel(input.Draft); err != nil {
		return LevelView{}, ErrInvalid
	}
	if id != "" && !db.ValidateOpaqueID(id, "ffl_") {
		return LevelView{}, ErrInvalid
	}
	var expected int64
	var err error
	if id != "" {
		expected, err = parseRevision(input.ExpectedRevision)
		if err != nil {
			return LevelView{}, err
		}
	} else if input.ExpectedRevision != "" {
		return LevelView{}, ErrInvalid
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return LevelView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LevelView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return LevelView{}, err
	}
	draftHash := sha256.Sum256(input.Draft)
	body := struct{ Title, Description, DraftHash, ExpectedRevision string }{input.Title, input.Description, hex.EncodeToString(draftHash[:]), input.ExpectedRevision}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPut, "/levels/{id}", []string{id}, body, nowMS)
	if err != nil {
		return LevelView{}, err
	}
	if replay, ok, replayErr := replayMutation[LevelView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	if id == "" {
		id, err = db.GenerateOpaqueID("ffl_")
		if err != nil {
			return LevelView{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_levels(id,title,description,draft_json,revision,actor_user_id,created_at,updated_at)
 VALUES(?,?,?,?,1,?,?,?)`, id, input.Title, input.Description, string(input.Draft), actorID, nowMS/1000, nowMS/1000)
	} else {
		var affected sql.Result
		affected, err = tx.ExecContext(ctx, `UPDATE fatfish_levels SET title=?,description=?,draft_json=?,revision=revision+1,actor_user_id=?,updated_at=?
 WHERE id=? AND revision=?`, input.Title, input.Description, string(input.Draft), actorID, nowMS/1000, id, expected)
		if err == nil {
			var count int64
			count, err = affected.RowsAffected()
			if err == nil && count != 1 {
				return LevelView{}, ErrConflict
			}
		}
	}
	if err != nil {
		return LevelView{}, err
	}
	view, err := readLevelTx(ctx, tx, id)
	if err != nil {
		return LevelView{}, err
	}
	view.Draft = nil // The editor fetches large draft content through GET.
	if err = completeMutationTx(ctx, tx, decision, view, http.StatusOK); err != nil {
		return LevelView{}, err
	}
	return view, tx.Commit()
}

func readLevelTx(ctx context.Context, tx *sql.Tx, id string) (LevelView, error) {
	var v LevelView
	var draft string
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT id,title,description,draft_json,revision,created_at,updated_at FROM fatfish_levels WHERE id=?`, id).Scan(&v.ID, &v.Title, &v.Description, &draft, &revision, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.Draft = json.RawMessage(draft)
	v.Revision = strconv.FormatInt(revision, 10)
	return v, nil
}

func (s *Service) Level(ctx context.Context, actorID int64, id string) (LevelView, error) {
	if !db.ValidateOpaqueID(id, "ffl_") {
		return LevelView{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LevelView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return LevelView{}, err
	}
	v, err := readLevelTx(ctx, tx, id)
	if err != nil {
		return LevelView{}, err
	}
	return v, tx.Commit()
}

// ExportLevel emits the exact document shape accepted by /levels/import.
// Identifiers, revisions, and timestamps belong to this installation only.
func (s *Service) ExportLevel(ctx context.Context, actorID int64, id string) (LevelInput, error) {
	level, err := s.Level(ctx, actorID, id)
	if err != nil {
		return LevelInput{}, err
	}
	return LevelInput{Title: level.Title, Description: level.Description, Draft: level.Draft}, nil
}

func (s *Service) Levels(ctx context.Context, actorID int64, page int) (LevelPage, error) {
	if !validCollectionPage(page) {
		return LevelPage{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LevelPage{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return LevelPage{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,title,description,revision,created_at,updated_at FROM fatfish_levels ORDER BY updated_at DESC,id LIMIT ? OFFSET ?`, collectionPageSize+1, (page-1)*collectionPageSize)
	if err != nil {
		return LevelPage{}, err
	}
	out := LevelPage{Items: []LevelView{}, Page: page, PageSize: collectionPageSize}
	for rows.Next() {
		var item LevelView
		var revision int64
		if err = rows.Scan(&item.ID, &item.Title, &item.Description, &revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return LevelPage{}, err
		}
		if len(out.Items) == collectionPageSize {
			out.HasMore = true
			break
		}
		item.Revision = strconv.FormatInt(revision, 10)
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return LevelPage{}, err
	}
	rows.Close()
	return out, tx.Commit()
}

func (s *Service) ValidateLevel(ctx context.Context, actorID int64, raw []byte) (VersionView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return VersionView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return VersionView{}, err
	}
	level, err := engine.ParseLevel(raw)
	if err != nil {
		return VersionView{}, ErrInvalid
	}
	content, err := engine.NormalizedLevel(level)
	if err != nil {
		return VersionView{}, err
	}
	hash, err := engine.ContentHash(level)
	if err != nil {
		return VersionView{}, err
	}
	v := VersionView{ContentHash: hash, EngineVersion: engine.EngineVersion, ScoringVersion: engine.ScoringVersion,
		DurationSeconds: level.DurationSeconds, MaximumStars: 3, Content: content}
	return v, tx.Commit()
}

func (s *Service) PublishVersion(ctx context.Context, actorID int64, levelID, expectedRevision, key string) (VersionView, error) {
	if !db.ValidateOpaqueID(levelID, "ffl_") {
		return VersionView{}, ErrInvalid
	}
	expected, err := parseRevision(expectedRevision)
	if err != nil {
		return VersionView{}, err
	}
	nowMS, err := s.nowMS()
	if err != nil {
		return VersionView{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VersionView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return VersionView{}, err
	}
	decision, err := s.beginMutationTx(ctx, tx, "admin", actorID, key, http.MethodPost, "/levels/{id}/versions", []string{levelID}, struct{ ExpectedRevision string }{expectedRevision}, nowMS)
	if err != nil {
		return VersionView{}, err
	}
	if replay, ok, replayErr := replayMutation[VersionView](decision); ok || replayErr != nil {
		return replay, replayErr
	}
	levelView, err := readLevelTx(ctx, tx, levelID)
	if err != nil {
		return VersionView{}, err
	}
	current, _ := parseRevision(levelView.Revision)
	if current != expected {
		return VersionView{}, ErrConflict
	}
	level, err := engine.ParseLevel(levelView.Draft)
	if err != nil {
		return VersionView{}, ErrInvalid
	}
	content, err := engine.NormalizedLevel(level)
	if err != nil {
		return VersionView{}, err
	}
	hash, err := engine.ContentHash(level)
	if err != nil {
		return VersionView{}, err
	}
	hashRaw, _ := hex.DecodeString(hash)
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM fatfish_level_versions WHERE level_id=? AND content_hash=? AND engine_version=? AND scoring_version=?`, levelID, hashRaw, engine.EngineVersion, engine.ScoringVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id, err = db.GenerateOpaqueID("ffv_")
		if err != nil {
			return VersionView{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO fatfish_level_versions(id,level_id,content_hash,engine_version,scoring_version,content_json,duration_seconds,maximum_stars,created_at)
 VALUES(?,?,?,?,?,?,?,?,?)`, id, levelID, hashRaw, engine.EngineVersion, engine.ScoringVersion, string(content), level.DurationSeconds, 3, nowMS/1000)
		if err != nil {
			return VersionView{}, err
		}
	} else if err != nil {
		return VersionView{}, err
	}
	v, err := readVersionTx(ctx, tx, id)
	if err != nil {
		return VersionView{}, err
	}
	v.Content = nil
	if err = completeMutationTx(ctx, tx, decision, v, http.StatusOK); err != nil {
		return VersionView{}, err
	}
	return v, tx.Commit()
}

func readVersionTx(ctx context.Context, tx *sql.Tx, id string) (VersionView, error) {
	var v VersionView
	var hash []byte
	var content string
	err := tx.QueryRowContext(ctx, `SELECT id,level_id,content_hash,engine_version,scoring_version,content_json,duration_seconds,maximum_stars,created_at FROM fatfish_level_versions WHERE id=?`, id).Scan(&v.ID, &v.LevelID, &hash, &v.EngineVersion, &v.ScoringVersion, &content, &v.DurationSeconds, &v.MaximumStars, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.ContentHash = hex.EncodeToString(hash)
	v.Content = json.RawMessage(content)
	return v, nil
}

func (s *Service) Version(ctx context.Context, actorID int64, id string) (VersionView, error) {
	if !db.ValidateOpaqueID(id, "ffv_") {
		return VersionView{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return VersionView{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return VersionView{}, err
	}
	v, err := readVersionTx(ctx, tx, id)
	if err != nil {
		return VersionView{}, err
	}
	return v, tx.Commit()
}

func (s *Service) Versions(ctx context.Context, actorID int64, levelID string, page int) (VersionPage, error) {
	if !db.ValidateOpaqueID(levelID, "ffl_") || !validCollectionPage(page) {
		return VersionPage{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return VersionPage{}, err
	}
	defer tx.Rollback()
	if err = s.authorizeAdminTx(ctx, tx, actorID); err != nil {
		return VersionPage{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,level_id,content_hash,engine_version,scoring_version,duration_seconds,maximum_stars,created_at FROM fatfish_level_versions WHERE level_id=? ORDER BY created_at DESC,id LIMIT ? OFFSET ?`, levelID, collectionPageSize+1, (page-1)*collectionPageSize)
	if err != nil {
		return VersionPage{}, err
	}
	out := VersionPage{Items: []VersionView{}, Page: page, PageSize: collectionPageSize}
	for rows.Next() {
		var item VersionView
		var hash []byte
		if err = rows.Scan(&item.ID, &item.LevelID, &hash, &item.EngineVersion, &item.ScoringVersion, &item.DurationSeconds, &item.MaximumStars, &item.CreatedAt); err != nil {
			rows.Close()
			return VersionPage{}, err
		}
		if len(out.Items) == collectionPageSize {
			out.HasMore = true
			break
		}
		item.ContentHash = hex.EncodeToString(hash)
		out.Items = append(out.Items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return VersionPage{}, err
	}
	rows.Close()
	return out, tx.Commit()
}
