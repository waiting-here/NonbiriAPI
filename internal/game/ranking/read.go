package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type Identity struct {
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}

func (i Identity) MarshalJSON() ([]byte, error) {
	if i.Kind != "public" {
		return []byte(`{"kind":"anonymous"}`), nil
	}
	return json.Marshal(struct {
		Kind   string  `json:"kind"`
		Name   string  `json:"display_name"`
		Avatar *string `json:"avatar_url"`
	}{i.Kind, i.DisplayName, i.AvatarURL})
}

type Row struct {
	Rank     string   `json:"rank"`
	Amount   string   `json:"amount"`
	IsMe     bool     `json:"is_me"`
	Identity Identity `json:"identity"`
}

type Board struct {
	AsOf                 int64                `json:"as_of"`
	StatisticsStart      int64                `json:"statistics_start"`
	Window               string               `json:"window"`
	Rows                 []Row                `json:"rows"`
	Me                   *Row                 `json:"me"`
	Pagination           *pagination.Metadata `json:"pagination,omitempty"`
	RebuildStatus        string               `json:"rebuild_status,omitempty"`
	HistoryCoverageStart *int64               `json:"history_coverage_start,omitempty"`
	MissingEvents        *string              `json:"missing_events,omitempty"`
}

// ReadTx uses current identity preferences, restrictions and role in the same
// snapshot as ranking and count. Internal owner IDs and tie keys never leave it.
func ReadTx(ctx context.Context, tx *sql.Tx, user int64, board, window string, page pagination.Request, now int64) (Board, error) {
	result := Board{AsOf: now, Window: window, Rows: make([]Row, 0, 20)}
	if err := tx.QueryRowContext(ctx, `SELECT started_at FROM game_statistics_epoch WHERE id=1`).Scan(&result.StatisticsStart); err != nil {
		return Board{}, err
	}
	if board == biddingBoard {
		rebuild, err := readBiddingRebuild(ctx, tx)
		if err != nil {
			return Board{}, err
		}
		result.RebuildStatus = rebuild.state
		if rebuild.coverage.Valid {
			result.HistoryCoverageStart = &rebuild.coverage.Int64
		}
		missing := strconv.FormatInt(rebuild.missing, 10)
		result.MissingEvents = &missing
		if rebuild.state != "completed" {
			return result, nil
		}
	}
	// Totals have a cascading owner foreign key. Excluding the indexed admin
	// identity avoids loading every preceding user row just to test its role.
	query := `SELECT t.user_id,t.amount_mag,t.achieved_at,t.achieved_phase,t.achieved_seq FROM game_rank_totals t WHERE t.board=? AND t.window=? AND t.amount_sign=1 AND t.user_id NOT IN (SELECT id FROM users WHERE is_admin=1)`
	args := []any{board, window}
	preference := `COALESCE(p.game_profile_public,u.game_profile_public)`
	var offset int64
	if board == "charity" {
		query = `SELECT id AS user_id,donation_credit_mag AS amount_mag,donation_credit_achieved_at AS achieved_at,0 AS achieved_phase,donation_credit_achieved_seq AS achieved_seq FROM users WHERE is_admin=0 AND donation_credit_mag>zeroblob(16)`
		args = nil
		preference = `u.charity_profile_public`
		// Both counts use existing narrow indexes; the subtraction is exact in
		// this transaction and avoids fetching every donor solely for is_admin.
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM users WHERE donation_credit_mag>zeroblob(16))-(SELECT COUNT(*) FROM users WHERE is_admin=1 AND donation_credit_mag>zeroblob(16))`).Scan(&count); err != nil {
			return Board{}, err
		}
		meta, start, err := page.Window(count)
		if err != nil {
			return Board{}, err
		}
		result.Pagination = &meta
		offset = start
	}
	const order = `amount_mag DESC,achieved_at,achieved_phase,achieved_seq`
	// Number only the selected page. The current user's position is an exact
	// count over preceding totals in this same identity and balance snapshot.
	// Achievement sequences identify globally unique, user-owned events. Expiry
	// retains a sequence from that user's events, so strict tuple precedence
	// also gives the exact row-number position when amounts and times tie.
	selected := db.MaterializePage(`SELECT * FROM eligible ORDER BY `+order+` LIMIT 20 OFFSET ?`, `ORDER BY `+order)
	query = `WITH eligible AS NOT MATERIALIZED (` + query + `), selected AS (` + selected + `),
 ranked AS (SELECT user_id,amount_mag,?+ROW_NUMBER() OVER(ORDER BY ` + order + `) AS rank FROM selected)`
	args = append(args, offset, offset)
	if board != "charity" {
		query += `, own AS (SELECT me.user_id,me.amount_mag,1+(SELECT COUNT(*) FROM eligible preceding
 WHERE preceding.amount_mag>=me.amount_mag AND (preceding.amount_mag>me.amount_mag OR
 (preceding.achieved_at,preceding.achieved_phase,preceding.achieved_seq)<(me.achieved_at,me.achieved_phase,me.achieved_seq))) AS rank
 FROM eligible me WHERE me.user_id=? AND NOT EXISTS(SELECT 1 FROM selected WHERE user_id=me.user_id)),
 visible AS (SELECT * FROM ranked UNION ALL SELECT * FROM own)`
		args = append(args, user)
	} else {
		query += `, visible AS (SELECT * FROM ranked)`
	}
	query += `
SELECT r.user_id,r.amount_mag,r.rank,CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE ` + preference + ` END,
u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM visible r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id ORDER BY r.rank`
	args = append(args, now)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return Board{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var owner, rank int64
		var mag []byte
		var public int
		var username, nick, discord, avatar, guild string
		if err := rows.Scan(&owner, &mag, &rank, &public, &username, &nick, &discord, &avatar, &guild); err != nil {
			return Board{}, err
		}
		amount, err := db.DecodeU128(mag)
		if err != nil {
			return Board{}, err
		}
		row := Row{strconv.FormatInt(rank, 10), credits(amount.Big()), owner == user, identity(public == 1, username, nick, discord, avatar, guild)}
		if board != "charity" && rank > 20 {
			result.Me = &row
		} else {
			result.Rows = append(result.Rows, row)
		}
	}
	return result, rows.Err()
}

func credits(milli *big.Int) string {
	negative := milli.Sign() < 0
	magnitude := new(big.Int).Abs(milli)
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(magnitude, big.NewInt(1000), remainder)
	result := whole.String()
	if remainder.Sign() != 0 {
		fraction := strconv.FormatInt(remainder.Int64()+1000, 10)[1:]
		result += "." + strings.TrimRight(fraction, "0")
	}
	if negative {
		return "-" + result
	}
	return result
}

func identity(public bool, username, nick, discord, avatar, guild string) Identity {
	return ProfileIdentity(public, username, nick, discord, avatar, guild)
}

// ProfileIdentity applies the shared text and image-origin rules to a public
// game profile. Callers supply the current preference and account eligibility.
func ProfileIdentity(public bool, username, nick, discord, avatar, guild string) Identity {
	if !public {
		return Identity{Kind: "anonymous"}
	}
	if strings.TrimSpace(nick) == "" {
		nick = username
	}
	if !utf8.ValidString(nick) {
		return Identity{Kind: "anonymous"}
	}
	name := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(nick)))
	name = name[:min(len(name), 128)]
	if len(name) == 0 {
		return Identity{Kind: "anonymous"}
	}
	result := Identity{Kind: "public", DisplayName: string(name)}
	if parsed, err := url.Parse(guild); err == nil && len(guild) <= 2048 && parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" && (parsed.Hostname() == "cdn.discordapp.com" || parsed.Hostname() == "media.discordapp.net") {
		result.AvatarURL = &guild
	} else if safeAtom(discord) && safeAtom(avatar) {
		result.AvatarURL = new("https://cdn.discordapp.com/avatars/" + discord + "/" + avatar + ".png")
	}
	return result
}

func safeAtom(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.", r)) {
			return false
		}
	}
	return true
}
