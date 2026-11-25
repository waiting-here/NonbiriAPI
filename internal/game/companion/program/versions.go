package program

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/kit"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/official"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

// A compile includes a smoke decision in the same single shared admission
// slot. Game decisions have their own bounded queue.
var compiling = make(chan struct{}, 1)

func (s *Service) compile(ctx context.Context, game, source string) ([]byte, string, error) {
	if !runner.UploadsEnabledFor(game) {
		return nil, "", runner.ErrUploadsDisabled
	}
	if s.worker.Path == "" {
		return nil, "", resources.ErrUnavailable
	}
	select {
	case compiling <- struct{}{}:
		defer func() { <-compiling }()
	default:
		return nil, "", resources.ErrResourceLimit
	}
	code, diag, err := s.worker.Compile(ctx, source)
	if errors.Is(err, runner.ErrProgram) {
		return nil, diag, nil
	}
	if err != nil {
		return nil, "", resources.ErrUnavailable
	}
	sample, err := kit.Sample(game)
	if err != nil {
		return nil, "", err
	}
	catalog, err := kit.Catalog(game, "standard")
	if err != nil {
		return nil, "", err
	}
	trial, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, err := s.worker.Decide(trial, runner.Job{Module: code, Request: sample, Catalog: catalog}, nil)
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	if errors.Is(err, runner.ErrWorker) {
		return nil, "", resources.ErrUnavailable
	}
	if err != nil {
		return nil, "The program could not complete the sample decision.", nil
	}
	var req official.Request
	var reply struct {
		Protocol string          `json:"protocol"`
		ID       string          `json:"decision_id"`
		Action   json.RawMessage `json:"action"`
		Memory   *string         `json:"memory"`
	}
	if json.Unmarshal(sample, &req) != nil {
		return nil, "", resources.ErrInvalidRequest
	}
	if json.Unmarshal(body, &reply) != nil || reply.Protocol != req.Protocol || reply.ID != req.ID || len(reply.Action) == 0 || reply.Memory != nil && len(*reply.Memory) > 8192 {
		return nil, "The sample reply does not match its decision window.", nil
	}
	if game == "likes" {
		if !validLikesSample(sample, reply.Action) {
			return nil, "The program returned an illegal plan for the sample.", nil
		}
	} else {
		var got map[string]any
		if json.Unmarshal(reply.Action, &got) != nil {
			return nil, "Expected one of the sample actions.", nil
		}
		canonical, _ := json.Marshal(got)
		found := false
		for _, raw := range req.Space.Examples {
			var candidate map[string]any
			if json.Unmarshal(raw, &candidate) != nil {
				continue
			}
			normalized, _ := json.Marshal(candidate)
			if bytes.Equal(normalized, canonical) {
				found = true
				break
			}
		}
		if !found {
			return nil, "The program returned an illegal sample action.", nil
		}
	}
	return code, "", nil
}

func lookupProgram(ctx context.Context, tx *sql.Tx, user int64, game, name string) (int64, error) {
	var owner any
	if user != 0 {
		owner = user
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM companion_programs WHERE user_id IS ? AND game=? AND name=?`, owner, game, name).Scan(&id)
	return id, err
}

func ensureProgram(ctx context.Context, tx *sql.Tx, user int64, game, name string) (int64, error) {
	id, err := lookupProgram(ctx, tx, user, game, name)
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	var owner any
	if user != 0 {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM companion_programs WHERE user_id=? AND game=?`, user, game).Scan(&n); err != nil {
			return 0, err
		}
		if n >= maxPrograms {
			return 0, resources.ErrResourceLimit
		}
		owner = user
	}
	row, err := tx.ExecContext(ctx, `INSERT INTO companion_programs(user_id,game,name) VALUES(?,?,?)`, owner, game, name)
	if err != nil {
		return 0, err
	}
	return row.LastInsertId()
}

const unpinned = `v.version<>p.active_version AND NOT EXISTS(SELECT 1 FROM companion_matches m
 WHERE m.status='active' AND m.game=p.game AND m.program=p.name AND m.version=v.version
 AND ((p.user_id IS NULL AND m.official=1) OR m.user_id=p.user_id AND m.official=0))`

func (s *Service) save(ctx context.Context, tx *sql.Tx, user int64, game, name, source string, code []byte, diag string) (sourceVersion, error) {
	result := sourceVersion{Name: name, Version: Version{Status: "failed", Diagnostic: diag, CreatedAt: s.Now().Unix()}}
	id, err := ensureProgram(ctx, tx, user, game, name)
	if err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE companion_programs SET last_version=last_version+1 WHERE id=? RETURNING last_version`, id).Scan(&result.Version.Version)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO companion_program_versions(program_id,version,source,module,diagnostic,created_at) VALUES(?,?,?,?,?,?)`, id, result.Version.Version, source, code, diag, result.CreatedAt); err != nil {
		return result, err
	}
	if code != nil {
		result.Status = "ready"
		// An administrator explicitly previews and activates an official upgrade.
		if user > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE companion_programs SET active_version=? WHERE id=?`, result.Version.Version, id); err != nil {
				return result, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM companion_program_versions WHERE program_id=? AND version IN (
	SELECT v.version FROM companion_program_versions v JOIN companion_programs p ON p.id=v.program_id WHERE p.id=? AND `+unpinned+`
	AND v.version <= (SELECT version FROM companion_program_versions WHERE program_id=? ORDER BY version DESC LIMIT 1 OFFSET ?))`, id, id, id, keepVersions)
	if err != nil {
		return result, err
	}
	var owner any
	if user > 0 {
		owner = user
	}
	var total, sourceBytes int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(v.module)),0),COALESCE(SUM(length(CAST(v.source AS BLOB))),0)
	FROM companion_programs p JOIN companion_program_versions v ON v.program_id=p.id WHERE p.user_id IS ? AND p.game=?`, owner, game).Scan(&total, &sourceBytes)
	if err == nil && (total > maxStoredBytes || sourceBytes > 4<<20) {
		err = resources.ErrResourceLimit
	}
	return result, err
}

func (s *Service) Retain(ctx context.Context, game string, now int64, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	const candidates = `SELECT v.rowid FROM companion_program_versions v JOIN companion_programs p ON p.id=v.program_id WHERE v.created_at<? AND p.game=? AND ` + unpinned + ` ORDER BY v.created_at LIMIT ?`
	cutoff := now - int64(versionAge/time.Second)
	var present bool
	if err := s.ReadDatabase.QueryRowContext(ctx, `SELECT EXISTS(`+candidates+`)`, cutoff, game, 1).Scan(&present); err != nil || !present {
		return 0, err
	}
	result, err := s.Database.ExecContext(ctx, `DELETE FROM companion_program_versions WHERE rowid IN (`+candidates+`)`, cutoff, game, limit)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

func (s *Service) activate(ctx context.Context, tx *sql.Tx, user int64, game, name string, version int64) error {
	if version < 0 || user > 0 && version == 0 {
		return resources.ErrInvalidRequest
	}
	id, err := ensureProgramForSelection(ctx, tx, user, game, name)
	if err != nil {
		return err
	}
	if version > 0 {
		var ready bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM companion_program_versions v JOIN companion_programs p ON p.id=v.program_id WHERE p.id=? AND v.version=? AND v.module IS NOT NULL AND (v.created_at>? OR NOT (`+unpinned+`)))`, id, version, s.Now().Add(-versionAge).Unix()).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return resources.ErrNotFound
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE companion_programs SET active_version=? WHERE id=?`, version, id)
	return err
}
func ensureProgramForSelection(ctx context.Context, tx *sql.Tx, user int64, game, name string) (int64, error) {
	if user == 0 {
		return ensureProgram(ctx, tx, user, game, builtin)
	}
	id, err := lookupProgram(ctx, tx, user, game, name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, resources.ErrNotFound
	}
	return id, err
}

func deleteProgram(ctx context.Context, tx *sql.Tx, user int64, game, name string) error {
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM companion_matches WHERE user_id=? AND game=? AND status='active' AND program=? AND official=0)`, user, game, name).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return resources.ErrConflict
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM companion_programs WHERE user_id=? AND game=? AND name=?`, user, game, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return resources.ErrNotFound
	}
	return err
}
func validSource(source string) bool {
	return len(source) > 0 && len(source) <= runner.SourceLimit && utf8.ValidString(source)
}
