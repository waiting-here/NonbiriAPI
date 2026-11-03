package attendance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/rewards"
)

type Service struct{ config Config }

func New(c Config) (*Service, error) {
	if c.Database == nil || c.Users == nil || c.Admins == nil || c.Gate == nil || c.Keys == nil {
		return nil, ErrInvalid
	}
	if c.ReadDatabase == nil {
		c.ReadDatabase = c.Database
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{config: c}, nil
}

func (s *Service) now() (int64, error) {
	at := s.config.Now().Unix()
	if at < 0 || at > 253402300799 {
		return 0, ErrInvalid
	}
	return at, nil
}

func activitiesEnabled(ctx context.Context, tx *sql.Tx) (bool, error) {
	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key='activities_enabled'").Scan(&value)
	if err != nil {
		return false, err
	}
	return value == "1", nil
}

func siteDate(at int64, offset int) string {
	return time.Unix(at, 0).In(time.FixedZone("site", offset*60)).Format(time.DateOnly)
}
func parseDate(value string, offset int) (time.Time, error) {
	t, err := time.ParseInLocation(time.DateOnly, value, time.FixedZone("site", offset*60))
	if err != nil || t.Format(time.DateOnly) != value {
		return time.Time{}, ErrInvalid
	}
	return t, nil
}

func normalize(in Input) (Input, error) {
	in.Title = strings.TrimSpace(strings.ReplaceAll(in.Title, "\r\n", "\n"))
	in.Description = strings.ReplaceAll(in.Description, "\r\n", "\n")
	if !utf8.ValidString(in.Title) || !utf8.ValidString(in.Description) || in.Title == "" || utf8.RuneCountInString(in.Title) > 80 || utf8.RuneCountInString(in.Description) > 2000 || strings.ContainsAny(in.Title, "\r\n") || strings.Contains(in.Description, "\r") {
		return in, fmt.Errorf("%w: title or description is too long or invalid", ErrInvalid)
	}
	if in.Mode != "cumulative" && in.Mode != "consecutive" {
		return in, fmt.Errorf("%w: choose cumulative or consecutive attendance", ErrInvalid)
	}
	if len(in.Tiers) < 1 || len(in.Tiers) > MaxTiers {
		return in, fmt.Errorf("%w: configure between 1 and 366 reward tiers", ErrInvalid)
	}
	in.Tiers = slices.Clone(in.Tiers)
	slices.SortFunc(in.Tiers, func(a, b TierInput) int { return a.Days - b.Days })
	count, last := 0, 0
	for _, tier := range in.Tiers {
		if tier.Days <= last || tier.Days > MaxDays {
			return in, fmt.Errorf("%w: reward days must be unique and between 1 and 3660", ErrInvalid)
		}
		if err := rewards.ValidateItems(tier.Rewards); err != nil {
			return in, fmt.Errorf("%w: check the reward assets and quantities", ErrInvalid)
		}
		count += len(tier.Rewards)
		last = tier.Days
	}
	if count > MaxRewardItems {
		return in, fmt.Errorf("%w: use at most 1024 reward items across the period", ErrInvalid)
	}
	return in, nil
}

func window(in Input, offset int) (int64, int64, error) {
	start, err := parseDate(in.StartDate, offset)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: use valid calendar dates", ErrInvalid)
	}
	end, err := parseDate(in.EndDate, offset)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: use valid calendar dates", ErrInvalid)
	}
	end = end.AddDate(0, 0, 1)
	days := (end.Unix() - start.Unix()) / 86400
	if start.Unix() < 0 || end.Unix() > 253402300800 || days < 1 || days > MaxDays || int64(in.Tiers[len(in.Tiers)-1].Days) > days {
		return 0, 0, fmt.Errorf("%w: the date range must cover the highest reward day and no more than 3660 days", ErrInvalid)
	}
	return start.Unix(), end.Unix(), nil
}

const periodColumns = "id,title,description,start_date,end_date,mode,published,revision,timezone_offset_minutes,created_at,starts_at,ends_at,actor_user_id"

type scanner interface{ Scan(...any) error }

func scanPeriod(row scanner) (Period, error) {
	var p Period
	err := row.Scan(&p.ID, &p.Title, &p.Description, &p.StartDate, &p.EndDate, &p.Mode, &p.Published, &p.Revision, &p.TimezoneOffsetMinutes, &p.CreatedAt, &p.startsAt, &p.endsAt, &p.actor)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return p, err
}
func readPeriod(ctx context.Context, tx *sql.Tx, id string) (Period, error) {
	return scanPeriod(tx.QueryRowContext(ctx, "SELECT "+periodColumns+" FROM attendance_periods WHERE id=?", id))
}

func viewTx(ctx context.Context, tx *sql.Tx, p Period, user, at int64, available bool) (Period, error) {
	p.SiteDate = siteDate(at, p.TimezoneOffsetMinutes)
	p.ConfigEditable = at < p.startsAt
	p.Status = "active"
	if at < p.startsAt {
		p.Status = "upcoming"
	} else if at >= p.endsAt {
		p.Status = "ended"
	}
	p.Available = available
	if user > 0 {
		var last string
		err := tx.QueryRowContext(ctx, "SELECT total_days,current_streak,best_streak,last_date FROM attendance_progress WHERE period_id=? AND user_id=?", p.ID, user).Scan(&p.TotalDays, &p.CurrentStreak, &p.BestStreak, &last)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return p, err
		}
		p.SignedToday = last == p.SiteDate
		// A missed calendar day ends only the current streak. Best progress is permanent.
		previous := siteDate(at-86400, p.TimezoneOffsetMinutes)
		if last < previous {
			p.CurrentStreak = 0
		}
		p.EligibleDays = p.TotalDays
		if p.Mode == "consecutive" {
			p.EligibleDays = p.BestStreak
		}
	}
	p.CanSignIn = user > 0 && available && p.Published && p.Status == "active" && !p.SignedToday
	p.Tiers = []Tier{}
	rows, err := tx.QueryContext(ctx, `SELECT t.days,t.rewards,a.results,a.awarded_at FROM attendance_tiers t
 LEFT JOIN attendance_awards a ON a.period_id=t.period_id AND a.days=t.days AND a.user_id=?
 WHERE t.period_id=? ORDER BY t.days LIMIT ?`, user, p.ID, MaxTiers+1)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Tier
		var raw string
		var result sql.NullString
		if err = rows.Scan(&t.Days, &raw, &result, &t.AwardedAt); err != nil {
			return p, err
		}
		if err = json.Unmarshal([]byte(raw), &t.Rewards); err != nil {
			return p, err
		}
		t.Awarded = t.AwardedAt != nil
		if result.Valid {
			if err = json.Unmarshal([]byte(result.String), &t.Results); err != nil {
				return p, err
			}
		}
		p.Tiers = append(p.Tiers, t)
	}
	if len(p.Tiers) > MaxTiers {
		return p, ErrInvalid
	}
	return p, rows.Err()
}

func (s *Service) List(ctx context.Context, actor int64, admin bool, page, size int) (Page, error) {
	out := Page{Items: []Period{}, Page: page, PageSize: size, Available: true}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, ErrInvalid
	}
	at, err := s.now()
	if err != nil {
		return out, err
	}
	tx, err := s.config.ReadDatabase.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if admin {
		err = s.config.Admins.AuthorizeAdmin(ctx, tx, actor)
	} else {
		err = s.config.Users.AuthorizeUserMutation(ctx, tx, actor)
	}
	if err != nil {
		return out, err
	}
	out.TimezoneOffsetMinutes, err = db.ResolveSiteTimezoneTx(ctx, tx)
	if err != nil {
		return out, err
	}
	out.SiteDate = siteDate(at, out.TimezoneOffsetMinutes)
	if !admin {
		out.Available, err = activitiesEnabled(ctx, tx)
		if err != nil {
			return out, err
		}
		if !out.Available {
			out.UnavailableReason = "disabled"
		}
		err = s.config.Gate.AuthorizeUserActivity(ctx, tx, actor)
		if errors.Is(err, maintenance.ErrMaintenanceOn) {
			out.Available = false
			out.UnavailableReason = "maintenance"
		} else if err != nil {
			return out, err
		}
	}
	where := ""
	user := actor
	if admin {
		user = 0
	} else {
		where = " WHERE published=1"
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM attendance_periods"+where).Scan(&out.Total); err != nil {
		return out, err
	}
	order := " ORDER BY start_date DESC,id DESC LIMIT ? OFFSET ?"
	args := []any{size, int64(page-1) * int64(size)}
	if !admin {
		// Order the bounded configuration directory, never attendance history.
		order = " ORDER BY CASE WHEN starts_at<=? AND ends_at>? THEN 0 WHEN starts_at>? THEN 1 ELSE 2 END,start_date DESC,id DESC LIMIT ? OFFSET ?"
		args = []any{at, at, at, size, int64(page-1) * int64(size)}
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+periodColumns+" FROM attendance_periods"+where+order, args...)
	if err != nil {
		return out, err
	}
	periods := []Period{}
	for rows.Next() {
		p, e := scanPeriod(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		periods = append(periods, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, p := range periods {
		p, err = viewTx(ctx, tx, p, user, at, out.Available)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, p)
	}
	return out, nil
}
