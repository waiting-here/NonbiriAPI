package blackjack

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

func liveIdentities(ctx context.Context, tx *sql.Tx, session string) ([]RealtimeIdentity, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.seat_no,u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url
FROM game_blackjack_entries e JOIN users u ON u.id=e.user_id
WHERE e.session_id=? AND e.seat_no IS NOT NULL AND e.state<>'released' ORDER BY e.seat_no`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identities := make([]RealtimeIdentity, 0, 9)
	for rows.Next() {
		var seat int
		var username, nick, discord, avatar, guild string
		if err := rows.Scan(&seat, &username, &nick, &discord, &avatar, &guild); err != nil {
			return nil, err
		}
		if seat < 0 || seat > 8 || len(identities) >= 9 {
			return nil, ErrInvariant
		}
		name := nick
		if strings.TrimSpace(name) == "" {
			name = username
		}
		name = cleanLiveName(name)
		if name == "" {
			continue
		}
		identities = append(identities, RealtimeIdentity{Seat: seat, DisplayName: name, AvatarURL: liveAvatar(discord, avatar, guild)})
	}
	return identities, rows.Err()
}

func cleanLiveName(s string) string {
	if !utf8.ValidString(s) {
		return ""
	}
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s)))
	if len(runes) > 128 {
		runes = runes[:128]
	}
	return strings.TrimSpace(string(runes))
}

func liveAvatar(discord, avatar, guild string) *string {
	if parsed, err := url.Parse(guild); err == nil && len(guild) <= 2048 && parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" && (parsed.Hostname() == "cdn.discordapp.com" || parsed.Hostname() == "media.discordapp.net") {
		return &guild
	}
	if safeLiveAtom(discord) && safeLiveAtom(avatar) {
		fallback := "https://cdn.discordapp.com/avatars/" + discord + "/" + avatar + ".png"
		return &fallback
	}
	return nil
}

func safeLiveAtom(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
