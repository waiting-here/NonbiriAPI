package program

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/kit"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/runner"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type endpoint func(http.ResponseWriter, *http.Request, int64)
type command struct {
	Name    string  `json:"name,omitempty"`
	Source  *string `json:"source,omitempty"`
	Version *int64  `json:"version,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
}

func (s *Service) RegisterRoutes(game string, r host.Registrars) error {
	if !validGame(game) {
		return resources.ErrInvalidRequest
	}
	base := "/api/games/" + game + "/companion"
	register := func(method, path string, admin bool, handler endpoint) error {
		if admin {
			return r.Admin.RegisterAdminRoute(method, "/admin"+base+path, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { handler(w, req, 0) }))
		}
		return r.User.RegisterUserRoute(method, base+path, func(w http.ResponseWriter, req *http.Request, p resources.UserPrincipal) { handler(w, req, p.UserID) })
	}
	for _, route := range []struct {
		method, path, op string
		admin            bool
	}{
		{"GET", "/programs", "programs", false}, {"GET", "/kit", "kit", false}, {"POST", "/programs", "upload", false},
		{"POST", "/programs/activate", "activate", false}, {"POST", "/programs/delete", "delete", false},
		{"GET", "", "programs", true}, {"POST", "/settings", "settings", true}, {"POST", "/versions", "upload", true}, {"POST", "/activate", "activate", true},
	} {
		err := register(route.method, route.path, route.admin, func(w http.ResponseWriter, req *http.Request, user int64) {
			if !runner.UploadsEnabledFor(game) && route.op != "settings" && (route.op != "programs" || req.URL.RawQuery != "") {
				writeError(w, runner.ErrUploadsDisabled)
				return
			}
			if route.method == "GET" {
				s.get(w, req, game, route.op, user)
				return
			}
			s.post(w, req, game, route.op, user)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	httperr.WriteMapped(w, err,
		httperr.Mapping{Err: runner.ErrUploadsDisabled, Code: httperr.CodeForbidden, Message: "Only the built-in companion is available in this release."},
		httperr.Mapping{Err: authz.ErrMFARequired, Code: httperr.CodeMFARequired, Message: "MFA verification is required."},
		httperr.Mapping{Err: authz.ErrUnauthorized, Code: httperr.CodeUnauthorized, Message: "authentication required"},
		httperr.Mapping{Err: resources.ErrUnauthorized, Code: httperr.CodeUnauthorized, Message: "authentication required"},
		httperr.Mapping{Err: authz.ErrForbidden, Code: httperr.CodeForbidden, Message: "forbidden"},
		httperr.Mapping{Err: resources.ErrForbidden, Code: httperr.CodeForbidden, Message: "forbidden"},
		httperr.Mapping{Err: resources.ErrMaintenance, Code: httperr.CodeMaintenance, Message: "maintenance mode"},
		httperr.Mapping{Err: resources.ErrInvalidRequest, Code: httperr.CodeInvalidRequest, Message: "invalid companion program request"},
		httperr.Mapping{Err: resources.ErrNotFound, Code: httperr.CodeNotFound, Message: "program or version not found"},
		httperr.Mapping{Err: resources.ErrConflict, Code: httperr.CodeConflict, Message: "program is in use or has no ready version"},
		httperr.Mapping{Err: idempotency.ErrConflict, Code: httperr.CodeConflict, Message: "idempotency key already used"},
		httperr.Mapping{Err: idempotency.ErrInProgress, Code: httperr.CodeConflict, Message: "request is already in progress"},
		httperr.Mapping{Err: resources.ErrResourceLimit, Code: httperr.CodeRateLimited, Message: "program storage limit reached or compiler busy"},
		httperr.Mapping{Err: resources.ErrUnavailable, Code: httperr.CodeServiceUnavailable, Message: "program upload or execution unavailable"})
}

func (s *Service) authorize(ctx context.Context, tx *sql.Tx, user int64) error {
	if user > 0 {
		return s.UserAuthorizer.AuthorizeUserMutation(ctx, tx, user)
	}
	return s.AdminAuthorizer.AuthorizeAdminMutation(ctx, tx)
}

func (s *Service) read(ctx context.Context, game string, user int64, name string, version int64) (any, error) {
	tx, err := s.ReadDatabase.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user); err != nil {
		return nil, err
	}
	if name != "" {
		return s.source(ctx, tx, user, game, name, version)
	}
	out, err := s.overview(ctx, tx, user, game)
	if err != nil || user > 0 {
		return out, err
	}
	return struct {
		index
		GlobalUploads bool `json:"upload_global_enabled"`
		GameUploads   bool `json:"upload_game_enabled"`
	}{out, runner.UploadsEnabled, runner.GameUploadsEnabled(game)}, nil
}

func (s *Service) get(w http.ResponseWriter, req *http.Request, game, op string, user int64) {
	if req.ContentLength > 0 {
		writeError(w, resources.ErrInvalidRequest)
		return
	}
	values, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		writeError(w, resources.ErrInvalidRequest)
		return
	}
	name := ""
	var version int64
	if req.URL.RawQuery != "" {
		name = values.Get("name")
		version, err = strconv.ParseInt(values.Get("version"), 10, 64)
		if user <= 0 || op != "programs" || len(values) != 2 || len(values["name"]) != 1 || len(values["version"]) != 1 || !validName(name) || err != nil || version < 1 {
			writeError(w, resources.ErrInvalidRequest)
			return
		}
	}
	if op == "kit" {
		// The regular user registrar has already authenticated this static download.
		data, err := kit.Archive(game)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+game+`-companion.zip"`)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(data)
		return
	}
	out, err := s.read(req.Context(), game, user, name, version)
	if err != nil {
		writeError(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

func decodeCommand(w http.ResponseWriter, r *http.Request, op string, admin bool) (command, error) {
	var c command
	if r.URL.RawQuery != "" || len(r.Header.Values("Idempotency-Key")) != 1 {
		return c, resources.ErrInvalidRequest
	}
	if _, err := idempotency.KeyHash(r.Header.Get("Idempotency-Key")); err != nil {
		return c, resources.ErrInvalidRequest
	}
	limit := int64(1024)
	if op == "upload" {
		limit = 6*runner.SourceLimit + 1024
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil || httpapi.DecodeJSON(body, &c) != nil {
		return c, resources.ErrInvalidRequest
	}
	if admin && c.Name != "" {
		return c, resources.ErrInvalidRequest
	}
	if admin {
		c.Name = builtin
	} else {
		c.Name = strings.TrimSpace(c.Name)
		if !validName(c.Name) {
			return c, resources.ErrInvalidRequest
		}
	}
	if (c.Source != nil) != (op == "upload") || (c.Enabled != nil) != (op == "settings") || (c.Version != nil) != (op == "activate") {
		return c, resources.ErrInvalidRequest
	}
	if c.Source != nil {
		source := strings.ReplaceAll(*c.Source, "\r\n", "\n")
		if !validSource(source) {
			return c, resources.ErrInvalidRequest
		}
		c.Source = &source
	}
	return c, nil
}

func (s *Service) post(w http.ResponseWriter, r *http.Request, game, op string, user int64) {
	c, err := decodeCommand(w, r, op, user == 0)
	if err != nil {
		writeError(w, err)
		return
	}
	var module []byte
	var diagnostic string
	if op == "upload" {
		// A replay can return its previous result even while the compiler is down.
		body, replayed, err := s.mutate(r, game, op, user, c, nil, "", true)
		if err != nil {
			writeError(w, err)
			return
		}
		if replayed {
			httperr.WriteJSON(w, http.StatusOK, body)
			return
		}
		module, diagnostic, err = s.compile(r.Context(), game, *c.Source)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	receipt, _, err := s.mutate(r, game, op, user, c, module, diagnostic, false)
	if err != nil {
		writeError(w, err)
		return
	}
	if op == "upload" {
		httperr.WriteJSON(w, http.StatusOK, receipt)
		return
	}
	out, err := s.read(r.Context(), game, user, "", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) mutate(r *http.Request, game, op string, user int64, c command, code []byte, diag string, probe bool) (json.RawMessage, bool, error) {
	ctx := r.Context()
	tx, err := s.Database.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user); err != nil {
		return nil, false, err
	}
	identity, kind := user, "user"
	if user == 0 {
		kind = "admin"
		if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin=1`).Scan(&identity); err != nil {
			return nil, false, err
		}
	}
	actor, _ := idempotency.ActorScopeHash(kind, strconv.FormatInt(identity, 10))
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, false, err
	}
	digest := sha256.Sum256(payload)
	requestHash, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: actor, Method: "POST", Route: r.URL.Path, Body: digest[:]})
	if err != nil {
		return nil, false, err
	}
	d, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: idempotency.Scope("game_" + game), ActorHash: actor, Key: r.Header.Get("Idempotency-Key"), RequestHash: requestHash, DecisionNow: s.Now().Unix()})
	if err != nil {
		return nil, false, err
	}
	if d.Kind == idempotency.Replay {
		return d.ResponseBody, true, nil
	}
	if probe {
		return nil, false, nil
	}
	var result any = struct {
		Updated bool `json:"updated"`
	}{true}
	switch op {
	case "upload":
		result, err = s.save(ctx, tx, user, game, c.Name, *c.Source, code, diag)
	case "activate":
		err = s.activate(ctx, tx, user, game, c.Name, *c.Version)
	case "delete":
		err = deleteProgram(ctx, tx, user, game, c.Name)
	case "settings":
		var id int64
		id, err = ensureProgram(ctx, tx, 0, game, builtin)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE companion_programs SET enabled=? WHERE id=?`, *c.Enabled, id)
		}
	default:
		err = resources.ErrInvalidRequest
	}
	if err != nil {
		return nil, false, err
	}
	body, err := json.Marshal(result)
	if err == nil {
		err = idempotency.Complete(ctx, tx, d, http.StatusOK, body)
	}
	if err == nil {
		err = tx.Commit()
	}
	return body, false, err
}
