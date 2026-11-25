package companion

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	gamecore "github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/game/companion/routes"
	"github.com/waiting-here/NonbiriAPI/internal/game/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) register(game string, r host.Registrars) error {
	if err := s.programs.RegisterRoutes(game, r); err != nil {
		return err
	}
	base := "/api/games/" + game + "/companion"
	for _, rt := range routes.Match(game) {
		op := strings.TrimPrefix(rt.Pattern, base)
		if err := r.User.RegisterUserRoute(rt.Method, rt.Pattern, func(w http.ResponseWriter, req *http.Request, p resources.UserPrincipal) {
			v, err := s.request(req, p.UserID, game, op)
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				writeError(w, err)
				return
			}
			httperr.WriteJSON(w, 200, v)
		}); err != nil {
			return err
		}
	}
	return nil
}
func writeError(w http.ResponseWriter, err error) {
	code, msg := httperr.CodeInternal, "Game request failed."
	switch {
	case errors.Is(err, authz.ErrMFARequired):
		code, msg = httperr.CodeMFARequired, "MFA verification is required."
	case errors.Is(err, gamecore.ErrStartRateLimited):
		code, msg = httperr.CodeRateLimited, "Game start limit reached."
	case errors.Is(err, errInput), errors.Is(err, resources.ErrInvalidRequest):
		code, msg = httperr.CodeInvalidRequest, "Invalid setup or game action."
	case errors.Is(err, errConflict), errors.Is(err, resources.ErrConflict):
		code, msg = httperr.CodeConflict, "The match or decision has changed."
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, resources.ErrNotFound):
		code, msg = httperr.CodeNotFound, "Match or program not found."
	case errors.Is(err, resources.ErrUnauthorized):
		code, msg = httperr.CodeUnauthorized, "Authentication required."
	case errors.Is(err, resources.ErrForbidden):
		code, msg = httperr.CodeForbidden, "Forbidden."
	case errors.Is(err, resources.ErrMaintenance):
		code, msg = httperr.CodeMaintenance, "Maintenance mode."
	case errors.Is(err, errDisabled), errors.Is(err, resources.ErrUnavailable):
		code, msg = httperr.CodeServiceUnavailable, "Companion play is unavailable."
	}
	httperr.WriteError(w, httperr.New(code, msg))
}
func readBody(r *http.Request) (Raw, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(b) > 65536 {
		return nil, errInput
	}
	return b, nil
}
func (s *Service) request(r *http.Request, user int64, game, op string) (any, error) {
	ctx := r.Context()
	read := r.Method == "GET"
	var body Raw
	if !read {
		var err error
		body, err = readBody(r)
		if err != nil {
			return nil, err
		}
	}
	database := s.shared.Database
	if read {
		database = s.shared.ReadDatabase
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: read})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.shared.UserAuthorizer.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return nil, err
	}
	id := r.PathValue("id")
	var m match
	if id != "" {
		m, err = readMatch(ctx, tx, id, game, user)
		if err != nil {
			return nil, err
		}
		if m.Ended > 0 && m.Ended <= s.shared.Now().Unix()-retention {
			return nil, sql.ErrNoRows
		}
	}
	if len(r.URL.RawQuery) > 0 && op != "/history" && op != "/matches/{id}/replay" {
		return nil, errInput
	}
	switch op {
	case "":
		p, err := getPrefs(ctx, tx, user, game)
		if err != nil {
			return nil, err
		}
		out := Obj{"preferences": p, "active": nil, "latest": nil}
		for _, item := range []struct{ key, filter string }{{"active", "status='active'"}, {"latest", "ended_at>0 AND ended_at>?"}} {
			args := []any{user, game}
			if item.key == "latest" {
				args = append(args, s.shared.Now().Unix()-retention)
			}
			var mid string
			err = tx.QueryRowContext(ctx, "SELECT id FROM companion_matches WHERE user_id=? AND game=? AND "+item.filter+" ORDER BY ended_at DESC,id DESC LIMIT 1", args...).Scan(&mid)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			v, err := readMatch(ctx, tx, mid, game, user)
			if err != nil {
				return nil, err
			}
			out[item.key], err = v.view(v.Seat)
			if err != nil {
				return nil, err
			}
			break
		}
		return out, nil
	case "/preferences":
		var p preferences
		if strict(body, &p) != nil || p.Memory != "none" && p.Memory != "shared" && p.Memory != "opponent" {
			return nil, errInput
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO companion_preferences(user_id,game,memory,contribute) VALUES(?,?,?,?) ON CONFLICT(user_id,game) DO UPDATE SET memory=excluded.memory,contribute=excluded.contribute`, user, game, p.Memory, p.Contribute)
		if err != nil {
			return nil, err
		}
		return p, tx.Commit()
	case "/history":
		page, size := 1, 20
		q := r.URL.Query()
		for k, values := range q {
			if len(values) != 1 {
				return nil, errInput
			}
			i, err := strconv.Atoi(values[0])
			if err != nil || i < 1 {
				return nil, errInput
			}
			switch k {
			case "page":
				page = i
			case "page_size":
				size = i
			default:
				return nil, errInput
			}
		}
		if page > 1000000 || size > 100 {
			return nil, errInput
		}
		return s.historyPage(ctx, tx, game, user, page, size)
	case "/matches/{id}/replay":
		if m.Status == "active" {
			return nil, errConflict
		}
		step := 0
		q := r.URL.Query()
		for k, v := range q {
			if k != "step" || len(v) != 1 {
				return nil, errInput
			}
			step, err = strconv.Atoi(v[0])
			if err != nil {
				return nil, errInput
			}
		}
		tx.Rollback()
		steps := len(m.Record.Actions) + 1
		f, h, err := replayAt(ctx, m.Record, step)
		if err != nil {
			return nil, err
		}
		m.Frame = f
		m.Record = h
		m.result = h.Result
		m.AIState = "idle"
		v, err := m.view(m.Seat)
		if err != nil {
			return nil, err
		}
		v.Decision = ""
		return Obj{"step": step, "steps": steps, "match": v}, nil
	case "/matches/{id}":
		return m.view(m.Seat)
	case "/matches":
		var in start
		if err := strict(body, &in); err != nil {
			return nil, errInput
		}
		m, err = s.create(ctx, tx, user, game, r.Header.Get("Idempotency-Key"), in)
		if m.startReservation != nil {
			defer m.startReservation.Release()
		}
	case "/matches/{id}/actions":
		var action struct {
			Decision string `json:"decision_id"`
			Action   Raw    `json:"action"`
		}
		if strict(body, &action) != nil {
			return nil, errInput
		}
		if m.Status != "active" || m.decision(m.Seat) != action.Decision {
			return nil, errConflict
		}
		gr, e := rulesFor(game, m.Mode)
		if e != nil {
			return nil, e
		}
		err = m.apply(gr, m.Seat, action.Action)
		if err != nil && !errors.Is(err, errInput) && !errors.Is(err, errConflict) {
			m, err = readMatch(ctx, tx, id, game, user)
			if err != nil {
				return nil, err
			}
			m.result = result{Status: "technical", Reason: "engine_error"}
		}
		if err == nil {
			err = m.update(ctx, tx, s.shared.Now())
		}
	case "/matches/{id}/abandon":
		if strict(body, &struct{}{}) != nil {
			return nil, errInput
		}
		if m.Status == "active" {
			m.result = result{Status: "abandoned", Reason: "abandoned"}
			err = m.update(ctx, tx, s.shared.Now())
		}
	default:
		return nil, errInput
	}
	if err != nil {
		return nil, err
	}
	v, err := m.view(m.Seat)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if m.startReservation != nil {
		m.startReservation.Commit()
	}
	if op == "/matches/{id}/abandon" {
		s.cancelGame(m.ID)
	}
	if m.AIState == "queued" {
		s.signal()
	}
	return v, nil
}
func (s *Service) historyPage(ctx context.Context, tx *sql.Tx, game string, user int64, page, size int) (any, error) {
	cutoff := s.shared.Now().Unix() - retention
	var total int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM companion_matches WHERE user_id=? AND game=? AND ended_at>0 AND ended_at>?", user, game, cutoff).Scan(&total)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+summaryCols+" FROM companion_matches WHERE user_id=? AND game=? AND ended_at>0 AND ended_at>? ORDER BY ended_at DESC,id DESC LIMIT ? OFFSET ?", user, game, cutoff, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []summary{}
	for rows.Next() {
		var item summary
		if err := rows.Scan(item.destinations()...); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return Obj{"total": total, "items": items}, rows.Err()
}
