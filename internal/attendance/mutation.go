package attendance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
)

func (s *Service) replay(ctx context.Context, tx *sql.Tx, actor int64, admin bool, key, method, route, id string, body any, at int64) (idempotency.Decision, error) {
	kind := "user"
	scope := idempotency.ScopeActivity
	if admin {
		kind = "admin"
		scope = idempotency.ScopeControlMutation
	}
	a, err := idempotency.ActorScopeHash(kind, strconv.FormatInt(actor, 10))
	if err != nil {
		return idempotency.Decision{}, err
	}
	raw, err := idempotency.CanonicalJSON(body)
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{ActorScopeHash: a, Method: method, Route: route, PathResourceIDs: []string{id}, Body: raw})
	if err != nil {
		return idempotency.Decision{}, ErrInvalid
	}
	secret, err := s.config.Keys.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/attendance-idempotency/v1"))
	if err != nil {
		return idempotency.Decision{}, err
	}
	defer clear(secret)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(digest[:])
	copy(digest[:], mac.Sum(nil))
	d, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{Scope: scope, ActorHash: a, Key: key, RequestHash: digest, DecisionNow: at})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		err = ErrConflict
	}
	return d, err
}
func complete(ctx context.Context, tx *sql.Tx, d idempotency.Decision, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return idempotency.Complete(ctx, tx, d, http.StatusOK, body)
}
func writeTiers(ctx context.Context, tx *sql.Tx, id string, tiers []TierInput) error {
	statement, err := tx.PrepareContext(ctx, "INSERT INTO attendance_tiers(period_id,days,rewards) VALUES(?,?,?)")
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, t := range tiers {
		raw, err := json.Marshal(t.Rewards)
		if err != nil {
			return err
		}
		if _, err = statement.ExecContext(ctx, id, t.Days, string(raw)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Create(ctx context.Context, actor int64, key string, input Input) (Period, error) {
	var out Period
	if _, err := idempotency.KeyHash(key); err != nil {
		return out, ErrInvalid
	}
	in, err := normalize(input)
	if err != nil {
		return out, err
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	d, err := s.replay(ctx, tx, actor, true, key, http.MethodPost, adminRoute, "", in, at)
	if err != nil {
		return out, err
	}
	var receipt struct {
		ID string `json:"id"`
	}
	if d.Kind == idempotency.Replay {
		if err = json.Unmarshal(d.ResponseBody, &receipt); err != nil {
			return out, err
		}
		out, err = readPeriod(ctx, tx, receipt.ID)
		if err == nil {
			out, err = viewTx(ctx, tx, out, 0, at, true)
		}
		if err != nil {
			return out, err
		}
		return out, managementlog.Commit(ctx, tx)
	}
	offset, err := db.ResolveSiteTimezoneTx(ctx, tx)
	if err != nil {
		return out, err
	}
	start, end, err := window(in, offset)
	if err != nil {
		return out, err
	}
	if in.StartDate <= siteDate(at, offset) {
		return out, fmt.Errorf("%w: the start date must be after today in site time", ErrInvalid)
	}
	id, err := db.GenerateOpaqueID("att_")
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attendance_periods(id,title,description,start_date,end_date,timezone_offset_minutes,starts_at,ends_at,mode,published,actor_user_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, in.Title, in.Description, in.StartDate, in.EndDate, offset, start, end, in.Mode, in.Published, actor, at, at)
	if err != nil {
		return out, err
	}
	if err = writeTiers(ctx, tx, id, in.Tiers); err != nil {
		return out, err
	}
	if err = db.FreezeSiteTimezoneTx(ctx, tx, at); err != nil {
		return out, err
	}
	out, err = readPeriod(ctx, tx, id)
	if err == nil {
		out, err = viewTx(ctx, tx, out, 0, at, true)
	}
	if err != nil {
		return out, err
	}
	receipt.ID = id
	if err = complete(ctx, tx, d, receipt); err != nil {
		return out, err
	}
	raw, _ := json.Marshal(in)
	managementlog.ChangedFields(ctx, raw)
	managementlog.SetTarget(ctx, "attendance_period", id, in.Title)
	return out, managementlog.Commit(ctx, tx)
}
func (s *Service) Update(ctx context.Context, actor int64, id, key string, input Edit) (Period, error) {
	var out Period
	if !db.ValidateOpaqueID(id, "att_") || input.ExpectedRevision < 1 || input.ExpectedRevision >= 9007199254740991 {
		return out, ErrInvalid
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		return out, ErrInvalid
	}
	in, err := normalize(input.Input)
	if err != nil {
		return out, err
	}
	input.Input = in
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor); err != nil {
		return out, err
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	d, err := s.replay(ctx, tx, actor, true, key, http.MethodPut, adminPeriodRoute, id, input, at)
	if err != nil {
		return out, err
	}
	out, err = readPeriod(ctx, tx, id)
	if err == nil {
		out, err = viewTx(ctx, tx, out, 0, at, true)
	}
	if err != nil {
		return out, err
	}
	if d.Kind == idempotency.Replay {
		return out, managementlog.Commit(ctx, tx)
	}
	if out.Revision != input.ExpectedRevision {
		return out, ErrConflict
	}
	start, end, err := window(in, out.TimezoneOffsetMinutes)
	if err != nil {
		return out, err
	}
	if !out.ConfigEditable {
		tiers := make([]TierInput, len(out.Tiers))
		for i, t := range out.Tiers {
			tiers[i] = t.TierInput
		}
		if in.StartDate != out.StartDate || in.EndDate != out.EndDate || in.Mode != out.Mode || in.Published != out.Published || !reflect.DeepEqual(in.Tiers, tiers) {
			return out, fmt.Errorf("%w: after the start date only the title and description can change", ErrConflict)
		}
	} else if in.StartDate <= out.SiteDate {
		return out, fmt.Errorf("%w: the start date must be after today in site time", ErrInvalid)
	}
	_, err = tx.ExecContext(ctx, `UPDATE attendance_periods SET title=?,description=?,start_date=?,end_date=?,starts_at=?,ends_at=?,mode=?,published=?,revision=revision+1,updated_at=? WHERE id=?`, in.Title, in.Description, in.StartDate, in.EndDate, start, end, in.Mode, in.Published, at, id)
	if err != nil {
		return out, err
	}
	if out.ConfigEditable {
		if _, err = tx.ExecContext(ctx, "DELETE FROM attendance_tiers WHERE period_id=?", id); err != nil {
			return out, err
		}
		if err = writeTiers(ctx, tx, id, in.Tiers); err != nil {
			return out, err
		}
	}
	if err = complete(ctx, tx, d, struct {
		ID string `json:"id"`
	}{id}); err != nil {
		return out, err
	}
	out, err = readPeriod(ctx, tx, id)
	if err == nil {
		out, err = viewTx(ctx, tx, out, 0, at, true)
	}
	if err != nil {
		return out, err
	}
	raw, _ := json.Marshal(input)
	managementlog.ChangedFields(ctx, raw)
	managementlog.SetTarget(ctx, "attendance_period", id, in.Title)
	return out, managementlog.Commit(ctx, tx)
}

func readAwards(ctx context.Context, tx *sql.Tx, period string, user int64, date string) ([]Award, error) {
	out := []Award{}
	rows, err := tx.QueryContext(ctx, "SELECT days,results,awarded_at FROM attendance_awards WHERE period_id=? AND user_id=? AND site_date=? ORDER BY days LIMIT ?", period, user, date, MaxTiers)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Award
		var raw string
		if err = rows.Scan(&a.Days, &raw, &a.AwardedAt); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(raw), &a.Results); err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) SignIn(ctx context.Context, user int64, id, key string, in SignInInput) (SignInResult, error) {
	var out SignInResult
	if !db.ValidateOpaqueID(id, "att_") {
		return out, ErrInvalid
	}
	if _, err := parseDate(in.SiteDate, 0); err != nil {
		return out, ErrInvalid
	}
	if _, err := idempotency.KeyHash(key); err != nil {
		return out, ErrInvalid
	}
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = s.config.Users.AuthorizeUserMutation(ctx, tx, user); err != nil {
		return out, err
	}
	if err = s.config.Gate.AuthorizeUserActivity(ctx, tx, user); err != nil {
		return out, err
	}
	enabled, err := activitiesEnabled(ctx, tx)
	if err != nil {
		return out, err
	}
	if !enabled {
		return out, ErrClosed
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	d, err := s.replay(ctx, tx, user, false, key, http.MethodPost, signInRoute, id, in, at)
	if err != nil {
		return out, err
	}
	p, err := readPeriod(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if !p.Published {
		return out, ErrNotFound
	}
	if d.Kind == idempotency.Replay {
		if err = json.Unmarshal(d.ResponseBody, &out.SignInReceipt); err != nil {
			return out, err
		}
		out.Period, err = viewTx(ctx, tx, p, user, at, true)
		if err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	out.SignInReceipt = SignInReceipt{PeriodID: id, SiteDate: in.SiteDate, Awards: []Award{}}
	err = tx.QueryRowContext(ctx, "SELECT signed_at FROM attendance_sign_ins WHERE period_id=? AND user_id=? AND site_date=?", id, user, in.SiteDate).Scan(&out.SignedAt)
	if err == nil {
		out.AlreadySigned = true
		out.Awards, err = readAwards(ctx, tx, id, user, in.SiteDate)
		if err != nil {
			return out, err
		}
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if in.SiteDate != siteDate(at, p.TimezoneOffsetMinutes) {
			return out, fmt.Errorf("%w: the site date changed; refresh the activity before signing in", ErrConflict)
		}
		if at < p.startsAt || at >= p.endsAt {
			return out, ErrClosed
		}
		var total, streak, best int
		var last string
		err = tx.QueryRowContext(ctx, "SELECT total_days,current_streak,best_streak,last_date FROM attendance_progress WHERE period_id=? AND user_id=?", id, user).Scan(&total, &streak, &best, &last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if last >= in.SiteDate {
			return out, ErrConflict
		}
		total++
		if last == siteDate(at-86400, p.TimezoneOffsetMinutes) {
			streak++
		} else {
			streak = 1
		}
		best = max(best, streak)
		_, err = tx.ExecContext(ctx, `INSERT INTO attendance_progress(period_id,user_id,total_days,current_streak,best_streak,last_date) VALUES(?,?,?,?,?,?)
 ON CONFLICT(period_id,user_id) DO UPDATE SET total_days=excluded.total_days,current_streak=excluded.current_streak,best_streak=excluded.best_streak,last_date=excluded.last_date`, id, user, total, streak, best, in.SiteDate)
		if err != nil {
			return out, err
		}
		out.SignedAt = at
		if _, err = tx.ExecContext(ctx, "INSERT INTO attendance_sign_ins(period_id,user_id,site_date,signed_at) VALUES(?,?,?,?)", id, user, in.SiteDate, at); err != nil {
			return out, err
		}
		p, err = viewTx(ctx, tx, p, user, at, true)
		if err != nil {
			return out, err
		}
		for _, t := range p.Tiers {
			if t.Awarded || t.Days > p.EligibleDays {
				continue
			}
			results, err := rewards.GrantTx(ctx, tx, user, p.actor.Int64, at, p.Title, t.Rewards)
			if err != nil {
				return out, err
			}
			raw, err := json.Marshal(results)
			if err != nil {
				return out, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO attendance_awards(period_id,user_id,days,site_date,results,awarded_at) VALUES(?,?,?,?,?,?)", id, user, t.Days, in.SiteDate, string(raw), at); err != nil {
				return out, err
			}
			out.Awards = append(out.Awards, Award{Days: t.Days, Results: results, AwardedAt: at})
		}
		if s.config.Activity != nil {
			if err = s.config.Activity.RecordLimitedActivityTx(ctx, tx, user, at); err != nil {
				return out, err
			}
		}
	}
	if err = complete(ctx, tx, d, out.SignInReceipt); err != nil {
		return out, err
	}
	out.Period, err = viewTx(ctx, tx, p, user, at, true)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
