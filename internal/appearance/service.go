// Package appearance manages account-scoped site cosmetics.
package appearance

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
)

var ErrInvalid = errors.New("appearance: invalid selection")

type Definition struct {
	ID     string `json:"id"`
	Unlock string `json:"unlock"`
}

//go:embed catalog.json
var catalogJSON []byte

var catalog = func() []Definition {
	var values []Definition
	if err := json.Unmarshal(catalogJSON, &values); err != nil {
		panic(err)
	}
	return values
}()

type State struct {
	Selected  string   `json:"selected"`
	Unlocked  []string `json:"unlocked"`
	Visible   []string `json:"visible,omitempty"`
	UpdatedAt *int64   `json:"updated_at"`
}
type Config struct {
	Database *sql.DB
	Users    limitedactivities.UserAuthorizer
	Gate     limitedactivities.AdmissionGate
	Unlock   func(context.Context, *sql.Tx, int64, string) (bool, error)
	Visible  func(context.Context, *sql.Tx, string) (bool, error)
	Now      func() time.Time
}
type Service struct{ config Config }

func New(c Config) (*Service, error) {
	if c.Database == nil || c.Users == nil || c.Gate == nil || c.Unlock == nil || c.Visible == nil {
		return nil, ErrInvalid
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{config: c}, nil
}
func (s *Service) authorize(ctx context.Context, tx *sql.Tx, user int64) error {
	if user <= 0 {
		return authz.ErrUnauthorized
	}
	if err := s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return err
	}
	var admin, banned bool
	var until sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT is_admin,is_banned,banned_until FROM users WHERE id=?", user).Scan(&admin, &banned, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin || banned && (!until.Valid || until.Int64 > s.config.Now().Unix()) {
		return authz.ErrForbidden
	}
	return nil
}
func (s *Service) ExportUserTx(ctx context.Context, tx *sql.Tx, user int64) (State, error) {
	out := State{Selected: "default", Unlocked: []string{}}
	for _, d := range catalog {
		unlocked := d.Unlock == ""
		if !unlocked {
			var err error
			unlocked, err = s.config.Unlock(ctx, tx, user, d.Unlock)
			if err != nil {
				return out, err
			}
		}
		if unlocked {
			out.Unlocked = append(out.Unlocked, d.ID)
		}
	}
	var selected string
	var at int64
	err := tx.QueryRowContext(ctx, "SELECT selected,updated_at FROM user_appearance WHERE user_id=?", user).Scan(&selected, &at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.UpdatedAt = &at
		for _, id := range out.Unlocked {
			if id == selected {
				out.Selected = id
			}
		}
	}
	return out, nil
}

// Visibility is a catalogue projection, not part of the account export.
func (s *Service) stateTx(ctx context.Context, tx *sql.Tx, user int64) (State, error) {
	out, err := s.ExportUserTx(ctx, tx, user)
	if err != nil {
		return out, err
	}
	out.Visible = []string{}
	for _, d := range catalog {
		unlocked := false
		for _, id := range out.Unlocked {
			unlocked = unlocked || id == d.ID
		}
		visible := unlocked
		if !visible {
			visible, err = s.config.Visible(ctx, tx, d.Unlock)
			if err != nil {
				return out, err
			}
		}
		if visible {
			out.Visible = append(out.Visible, d.ID)
		}
	}
	return out, nil
}

func (s *Service) State(ctx context.Context, user int64) (State, error) {
	tx, err := s.config.Database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user); err != nil {
		return State{}, err
	}
	out, err := s.stateTx(ctx, tx, user)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// Select replaces the preference; repeating the same choice has no side effects.
func (s *Service) Select(ctx context.Context, user int64, selected string) (State, error) {
	known := false
	for _, d := range catalog {
		known = known || d.ID == selected
	}
	if !known {
		return State{}, ErrInvalid
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	if err = s.authorize(ctx, tx, user); err != nil {
		return State{}, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return State{}, err
	}
	out, err := s.stateTx(ctx, tx, user)
	if err != nil {
		return out, err
	}
	unlocked := false
	for _, id := range out.Unlocked {
		unlocked = unlocked || id == selected
	}
	if !unlocked {
		return State{}, authz.ErrForbidden
	}
	if out.Selected != selected {
		at := s.config.Now().Unix()
		_, err = tx.ExecContext(ctx, `INSERT INTO user_appearance(user_id,selected,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET selected=excluded.selected,updated_at=excluded.updated_at`, user, selected, at)
		if err != nil {
			return out, err
		}
		out.Selected = selected
		out.UpdatedAt = &at
	}
	return out, tx.Commit()
}
func DeleteUserTx(ctx context.Context, tx *sql.Tx, user int64) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM user_appearance WHERE user_id=?", user)
	return err
}
