package db

// Admin/user management repository: per-user limit/language updates (shared
// by the admin PATCH and the user self-service PATCH), the bounded admin
// user list, and runtime site_config persistence.
//
// Ownership and role decisions are made in SQL: the administrator row is
// excluded from lists and protected from mutation, every update targets
// exactly one normal user id, and no caller-key or session material is ever
// selected or projected here. Site_config values are bounded and
// control-character-free on both write and read; a manually corrupted row is
// skipped on read rather than projected.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxUserListPageSize bounds one admin user-list page.
	MaxUserListPageSize = 100
	// MaxUserEndpointLimit bounds the per-user endpoint cap. Zero is valid
	// (no new endpoints; existing endpoints are retained).
	MaxUserEndpointLimit = 10000
	// MaxUserRPMLimit is the independent hard range for an explicit per-user
	// RPM override. The site default is only a NULL fallback, never a cap.
	MaxUserRPMLimit = 4096
	// DefaultUserConcurrencyLimit is the built-in effective value for a NULL
	// users.concurrency_limit; there is deliberately no site-config key for it.
	DefaultUserConcurrencyLimit = 5
	// MaxUserConcurrencyLimit is the independent hard ceiling for an explicit
	// in-flight request override.
	MaxUserConcurrencyLimit = 100000
	// maxSiteConfigKeyBytes bounds site_config keys.
	maxSiteConfigKeyBytes = 128
	// maxSiteConfigValueBytes bounds site_config values. Legal overrides may
	// be multi-paragraph documents, so this is larger than the identity text
	// bound; per-key limits are still enforced by the adminapi registry.
	maxSiteConfigValueBytes = 65536
)

// UserLimitPatch is the tri-state update set for users.endpoint_limit,
// users.rpm_limit, users.concurrency_limit, users.lang, users.level and the
// account-wide game leaderboard privacy preference.
//
//   - EndpointLimitSet / RPMLimitSet / ConcurrencyLimitSet / LevelSet report
//     whether the field was present. A present limit with a nil pointer clears
//     it (NULL = its documented fallback); a nil Level restores automatic.
//   - LangSet selects a lang change ("zh" or "en" only; no NULL semantics).
//   - Level, when set, must be nil (reset to automatic) or within
//     [MinLevel, MaxLevel]; it never touches the auto_level high-water mark.
type UserLimitPatch struct {
	EndpointLimitSet     bool
	EndpointLimit        *int
	RPMLimitSet          bool
	RPMLimit             *int
	ConcurrencyLimitSet  bool
	ConcurrencyLimit     *int
	LangSet              bool
	Lang                 string
	LevelSet             bool
	Level                *int
	GameProfilePublicSet bool
	GameProfilePublic    bool
}

func (p UserLimitPatch) validate() error {
	if p.EndpointLimitSet && p.EndpointLimit != nil &&
		(*p.EndpointLimit < 0 || *p.EndpointLimit > MaxUserEndpointLimit) {
		return ErrConflict
	}
	if p.RPMLimitSet && p.RPMLimit != nil &&
		(*p.RPMLimit < 1 || *p.RPMLimit > MaxUserRPMLimit) {
		return ErrConflict
	}
	if p.ConcurrencyLimitSet && p.ConcurrencyLimit != nil &&
		(*p.ConcurrencyLimit < 1 || *p.ConcurrencyLimit > MaxUserConcurrencyLimit) {
		return ErrConflict
	}
	if p.LangSet && p.Lang != "zh" && p.Lang != "en" {
		return ErrConflict
	}
	if p.LevelSet && p.Level != nil && (*p.Level < MinLevel || *p.Level > MaxLevel) {
		return ErrConflict
	}
	return nil
}

// UpdateUserLimits applies one or more server-authoritative per-user
// limit/language changes and returns the refreshed user row. The target must
// be a normal (non-administrator) user; the administrator row is
// ErrAdminProtected. A missing user is ErrNotFound; invalid values and an
// empty patch are ErrConflict.
func (s *Store) UpdateUserLimits(userID int64, patch UserLimitPatch) (*User, error) {
	if userID <= 0 {
		return nil, ErrNotFound
	}
	if err := patch.validate(); err != nil {
		return nil, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("update user limits: %w", err)
	}
	defer tx.Rollback()

	var isAdmin int
	if err := tx.QueryRow(`SELECT is_admin FROM users WHERE id=?`, userID).Scan(&isAdmin); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("update user limits: %w", err)
	} else if isAdmin != 0 {
		return nil, ErrAdminProtected
	}

	// The SET clause is built exclusively from validated constant column
	// names; no request text ever becomes SQL.
	var sets []string
	var args []any
	if patch.EndpointLimitSet {
		sets = append(sets, "endpoint_limit=?")
		if patch.EndpointLimit == nil {
			args = append(args, nil)
		} else {
			args = append(args, *patch.EndpointLimit)
		}
	}
	if patch.RPMLimitSet {
		sets = append(sets, "rpm_limit=?")
		if patch.RPMLimit == nil {
			args = append(args, nil)
		} else {
			args = append(args, *patch.RPMLimit)
		}
	}
	if patch.ConcurrencyLimitSet {
		sets = append(sets, "concurrency_limit=?")
		if patch.ConcurrencyLimit == nil {
			args = append(args, nil)
		} else {
			args = append(args, *patch.ConcurrencyLimit)
		}
	}
	if patch.LangSet {
		sets = append(sets, "lang=?")
		args = append(args, patch.Lang)
	}
	if patch.LevelSet {
		sets = append(sets, "level=?")
		if patch.Level == nil {
			args = append(args, nil)
		} else {
			args = append(args, *patch.Level)
		}
	}
	if patch.GameProfilePublicSet {
		sets = append(sets, "game_profile_public=?")
		if patch.GameProfilePublic {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if len(sets) == 0 {
		return nil, ErrConflict
	}
	args = append(args, time.Now().Unix(), userID)
	// #nosec G202 -- sets contains only the four constant column fragments
	// selected above; every request-derived value remains a bound argument.
	if _, err := tx.Exec(`UPDATE users SET `+strings.Join(sets, ", ")+`, updated_at=? WHERE id=? AND is_admin=0`, args...); err != nil {
		return nil, fmt.Errorf("update user limits: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("update user limits: %w", err)
	}
	return s.GetUserByID(userID)
}

// UserListQuery is the bounded, parameterized admin user-list filter.
type UserListQuery struct {
	Page       int    // 1-based; values below 1 are treated as 1
	PageSize   int    // clamped to 1..MaxUserListPageSize; 0 selects the default
	BannedOnly *bool  // nil = every state; true = banned only; false = active only
	Q          string // optional substring filter on username/discord_id; "" = no filter
}

// ListUsers returns one page of normal users ordered by id ascending. The
// environment-owned administrator row is never a candidate. The second
// return value reports whether another page follows, so a client never
// infers pagination from a raw page size.
func (s *Store) ListUsers(ctx context.Context, query UserListQuery) ([]User, bool, error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > MaxUserListPageSize {
		pageSize = MaxUserListPageSize
	}

	sqlText := `SELECT ` + userSelectColumns + ` FROM users u WHERE u.is_admin=0`
	var args []any
	if query.BannedOnly != nil {
		if *query.BannedOnly {
			sqlText += ` AND u.is_banned=1`
		} else {
			sqlText += ` AND u.is_banned=0`
		}
	}
	if query.Q != "" {
		// Substring search on username or discord_id. The LIKE pattern escapes
		// the LIKE metacharacters (\, %, _) so user input can never widen the
		// match into a wildcard, and ESCAPE '\' activates the escape. The
		// leading % makes a btree index unusable, so none is added: at alpha
		// scale a full scan of the bounded user set is sufficient and an index
		// here would only mislead.
		pattern := likePattern(query.Q)
		sqlText += ` AND (u.username LIKE ? ESCAPE '\' OR u.discord_id LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}
	sqlText += ` ORDER BY u.id ASC LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0, min(pageSize, 32))
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate users: %w", err)
	}
	hasMore := len(users) > pageSize
	if hasMore {
		users = users[:pageSize]
	}
	return users, hasMore, nil
}

// likePattern escapes the LIKE metacharacters (\, %, _) in q so a literal
// user search string can never widen the match into a wildcard, then wraps it
// as a substring match (%q%). It must be paired with LIKE ... ESCAPE '\' so
// the backslash is treated as the escape character. strings.NewReplacer does
// all replacements in one non-overlapping pass and never re-scans its output,
// so the backslashes added for % and _ are not themselves escaped twice.
func likePattern(q string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + escaped + "%"
}

// SetSiteConfigValue upserts one runtime site_config row. Keys and values are
// bounded and control-character-free; an empty value is allowed (blank values
// pause the registration gate). Invalid input is ErrConflict.
func (s *Store) SetSiteConfigValue(key, value string) error {
	if err := validateSiteConfigText(key, maxSiteConfigKeyBytes, false, false); err != nil {
		return ErrConflict
	}
	if err := validateSiteConfigText(value, maxSiteConfigValueBytes, true, true); err != nil {
		return ErrConflict
	}
	if _, err := s.db.Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().Unix()); err != nil {
		return fmt.Errorf("set site configuration: %w", err)
	}
	return nil
}

// GetAllSiteConfigValues returns every runtime site_config row that passes
// the bounded-text rules. A manually corrupted row is skipped on read: it
// can never carry control characters to the wire, and it never breaks the
// whole projection. Callers filter the authoritative known-key set.
func (s *Store) GetAllSiteConfigValues() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM site_config`)
	if err != nil {
		return nil, fmt.Errorf("list site configuration: %w", err)
	}
	defer rows.Close()

	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan site configuration: %w", err)
		}
		if validateSiteConfigText(key, maxSiteConfigKeyBytes, false, false) != nil ||
			validateSiteConfigText(value, maxSiteConfigValueBytes, true, true) != nil {
			continue
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate site configuration: %w", err)
	}
	return values, nil
}

func validateSiteConfigText(value string, maxBytes int, allowEmpty bool, allowMultiline bool) error {
	if !allowEmpty && value == "" {
		return errors.New("site configuration text is required")
	}
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return errors.New("site configuration text is invalid")
	}
	for _, r := range value {
		if (unicode.IsControl(r) || r == 0x7f) && !(allowMultiline && (r == '\n' || r == '\r' || r == '\t')) {
			return errors.New("site configuration text is invalid")
		}
	}
	return nil
}
