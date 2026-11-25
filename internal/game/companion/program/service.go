// Package program binds a companion match to an immutable built-in or uploaded
// decision program. Source and compiler diagnostics belong to the author.
package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/historyio"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/official"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

const builtin = "official"
const maxPrograms = 8
const maxStoredBytes = 32 << 20
const keepVersions = 8
const versionAge = 30 * 24 * time.Hour

type Binding struct {
	Game       string `json:"game"`
	Name       string `json:"name"`
	OwnerID    int64  `json:"owner_id"`
	ProgramID  int64  `json:"program_id"`
	Version    int64  `json:"version"`
	Official   bool   `json:"official"`
	Difficulty int    `json:"difficulty"`
}

type Service struct {
	host.Services
	worker runner.Client
}

func New(shared host.Services) *Service {
	return &Service{Services: shared, worker: runner.Configured()}
}

func validGame(game string) bool { return game == "bidding" || game == "likes" || game == "gwent" }
func validName(name string) bool {
	return name != "" && name != builtin && strings.TrimSpace(name) == name && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 64 && strings.IndexFunc(name, unicode.IsControl) < 0
}

func (s *Service) ResolveTx(ctx context.Context, tx *sql.Tx, userID int64, game, name string, difficulty int) (Binding, error) {
	b := Binding{Game: game, Name: name, Difficulty: difficulty, Official: name == builtin}
	if !validGame(game) || difficulty != 1 || userID <= 0 || !b.Official && !validName(name) {
		return b, resources.ErrInvalidRequest
	}
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT enabled,active_version FROM companion_programs WHERE game=? AND user_id IS NULL`, game).Scan(&enabled, &b.Version)
	if errors.Is(err, sql.ErrNoRows) {
		enabled = true
		b.Version = 0
	} else if err != nil {
		return b, err
	}
	if !enabled {
		return b, resources.ErrUnavailable
	}
	if !b.Official {
		b.OwnerID = userID
		err = tx.QueryRowContext(ctx, `SELECT id,active_version FROM companion_programs WHERE user_id=? AND game=? AND name=?`, userID, game, name).Scan(&b.ProgramID, &b.Version)
		if errors.Is(err, sql.ErrNoRows) {
			return b, resources.ErrNotFound
		}
		if err != nil {
			return b, err
		}
		if b.Version == 0 {
			return b, resources.ErrConflict
		}
	}
	if b.Version > 0 && s.worker.Path == "" {
		return b, resources.ErrUnavailable
	}
	return b, nil
}

func (s *Service) Decide(ctx context.Context, b Binding, request, catalog json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.Official && b.Name == builtin && b.OwnerID == 0 && b.Version == 0 {
		return official.Decide(ctx, b.Game, request)
	}
	if b.Version < 1 || b.Official != (b.OwnerID == 0) || b.Official != (b.Name == builtin) {
		return nil, resources.ErrInvalidRequest
	}
	var owner any
	if !b.Official {
		owner = b.OwnerID
	}
	var code []byte
	err := s.ReadDatabase.QueryRowContext(ctx, `SELECT v.module FROM companion_program_versions v JOIN companion_programs p ON p.id=v.program_id WHERE p.user_id IS ? AND p.game=? AND p.name=? AND (p.user_id IS NULL OR p.id=?) AND v.version=? AND v.module IS NOT NULL`, owner, b.Game, b.Name, b.ProgramID, b.Version).Scan(&code)
	if err != nil {
		return nil, err
	}
	return s.worker.Decide(ctx, runner.Job{Module: code, Request: request, Catalog: catalog}, historyio.Call)
}

type Version struct {
	Version    int64  `json:"version"`
	Status     string `json:"status"`
	Diagnostic string `json:"diagnostic,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}
type entry struct {
	Name         string    `json:"name"`
	Version      int64     `json:"version"`
	Versions     []Version `json:"versions"`
	Difficulties []int     `json:"difficulties,omitempty"`
}
type index struct {
	Enabled  bool    `json:"enabled"`
	Upload   bool    `json:"upload_available"`
	Official entry   `json:"official"`
	Programs []entry `json:"programs"`
}
type sourceVersion struct {
	Name string `json:"name"`
	Version
	Source *string `json:"source,omitempty"`
}

func (s *Service) overview(ctx context.Context, tx *sql.Tx, user int64, game string) (index, error) {
	out := index{Enabled: true, Upload: s.worker.Path != "", Programs: []entry{}, Official: entry{Name: builtin, Difficulties: []int{1}, Versions: []Version{{Version: 0, Status: "ready"}}}}
	err := tx.QueryRowContext(ctx, `SELECT enabled,active_version FROM companion_programs WHERE game=? AND user_id IS NULL`, game).Scan(&out.Enabled, &out.Official.Version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	var owner any
	if user > 0 {
		owner = user
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.name,p.active_version,COALESCE(v.version,0),v.module IS NOT NULL,COALESCE(v.diagnostic,''),COALESCE(v.created_at,0)
		FROM companion_programs p LEFT JOIN companion_program_versions v ON v.program_id=p.id AND (v.created_at>? OR NOT (`+unpinned+`))
		WHERE p.user_id IS ? AND p.game=? ORDER BY p.name,v.version DESC`, s.Now().Add(-versionAge).Unix(), owner, game)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var active int64
		var ready bool
		var v Version
		if err := rows.Scan(&name, &active, &v.Version, &ready, &v.Diagnostic, &v.CreatedAt); err != nil {
			return out, err
		}
		v.Status = "failed"
		if ready {
			v.Status = "ready"
		}
		if user == 0 {
			if v.Version > 0 {
				out.Official.Versions = append(out.Official.Versions, v)
			}
			continue
		}
		if len(out.Programs) == 0 || out.Programs[len(out.Programs)-1].Name != name {
			out.Programs = append(out.Programs, entry{Name: name, Version: active, Versions: []Version{}})
		}
		p := &out.Programs[len(out.Programs)-1]
		if v.Version > 0 {
			p.Versions = append(p.Versions, v)
		}
	}
	return out, rows.Err()
}

func (s *Service) source(ctx context.Context, tx *sql.Tx, user int64, game, name string, version int64) (sourceVersion, error) {
	var r sourceVersion
	var ready bool
	err := tx.QueryRowContext(ctx, `SELECT p.name,v.version,v.module IS NOT NULL,v.diagnostic,v.created_at,v.source FROM companion_programs p JOIN companion_program_versions v ON v.program_id=p.id WHERE p.user_id=? AND p.game=? AND p.name=? AND v.version=? AND (v.created_at>? OR NOT (`+unpinned+`))`, user, game, name, version, s.Now().Add(-versionAge).Unix()).Scan(&r.Name, &r.Version.Version, &ready, &r.Diagnostic, &r.CreatedAt, &r.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return r, resources.ErrNotFound
	}
	r.Status = "failed"
	if ready {
		r.Status = "ready"
	}
	return r, err
}

func (s *Service) ExportTx(ctx context.Context, tx *sql.Tx, user int64, game string) (any, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.name,p.active_version,v.version,v.source,v.diagnostic,v.created_at,v.module IS NOT NULL FROM companion_programs p JOIN companion_program_versions v ON v.program_id=p.id WHERE p.user_id=? AND p.game=? AND (v.created_at>? OR NOT (`+unpinned+`)) ORDER BY p.name,v.version`, user, game, s.Now().Add(-versionAge).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type exported struct {
		sourceVersion
		Active bool `json:"active"`
	}
	out := []exported{}
	for rows.Next() {
		var v exported
		var active int64
		var ready bool
		if err := rows.Scan(&v.Name, &active, &v.Version.Version, &v.Source, &v.Diagnostic, &v.CreatedAt, &ready); err != nil {
			return nil, err
		}
		v.Active = active == v.Version.Version
		v.Status = "failed"
		if ready {
			v.Status = "ready"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
