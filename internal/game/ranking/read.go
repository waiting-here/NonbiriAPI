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
	AsOf            int64                `json:"as_of"`
	StatisticsStart int64                `json:"statistics_start"`
	Window          string               `json:"window"`
	Rows            []Row                `json:"rows"`
	Me              *Row                 `json:"me"`
	Pagination      *pagination.Metadata `json:"pagination,omitempty"`
}

// ReadTx uses current identity preferences, restrictions and role in the same
// snapshot as ranking and count. Internal owner IDs and tie keys never leave it.
func ReadTx(ctx context.Context, tx *sql.Tx, user int64, board, window string, page pagination.Request, now int64) (Board, error) {
	result := Board{AsOf: now, Window: window, Rows: make([]Row, 0, 20)}
	if err := tx.QueryRowContext(ctx, `SELECT started_at FROM game_statistics_epoch WHERE id=1`).Scan(&result.StatisticsStart); err != nil {
		return Board{}, err
	}
	query := `SELECT t.user_id,t.amount_mag,t.achieved_at,t.achieved_phase,t.achieved_seq FROM game_rank_totals t JOIN users u ON u.id=t.user_id WHERE t.board=? AND t.window=? AND t.amount_sign=1 AND u.is_admin=0`
	args := []any{board, window}
	preference := `COALESCE(p.game_profile_public,u.game_profile_public)`
	var offset int64
	if board == "charity" {
		query = `SELECT id AS user_id,donation_credit_mag AS amount_mag,donation_credit_achieved_at AS achieved_at,0 AS achieved_phase,donation_credit_achieved_seq AS achieved_seq FROM users WHERE is_admin=0 AND donation_credit_mag>zeroblob(16)`
		args = nil
		preference = `u.charity_profile_public`
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (`+query+`)`).Scan(&count); err != nil {
			return Board{}, err
		}
		meta, start, err := page.Window(count)
		if err != nil {
			return Board{}, err
		}
		result.Pagination = &meta
		offset = start
	}
	query = `WITH eligible AS (` + query + `), ranked AS (SELECT *,ROW_NUMBER() OVER(ORDER BY amount_mag DESC,achieved_at,achieved_phase,achieved_seq) AS rank FROM eligible)
SELECT r.user_id,r.amount_mag,r.rank,CASE WHEN u.is_banned=1 AND (u.banned_until IS NULL OR u.banned_until>?) THEN 0 ELSE ` + preference + ` END,
u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM ranked r JOIN users u ON u.id=r.user_id LEFT JOIN game_user_preferences p ON p.user_id=r.user_id WHERE `
	args = append(args, now)
	if board == "charity" {
		query += `r.rank>? AND r.rank<=?`
		args = append(args, offset, offset+20)
	} else {
		query += `r.rank<=20 OR r.user_id=?`
		args = append(args, user)
	}
	query += ` ORDER BY r.rank`
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
	if parsed, err := url.Parse(guild); err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
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
