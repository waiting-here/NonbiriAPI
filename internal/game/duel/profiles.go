package duel

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Profile struct {
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url,omitempty"`
}

// Profiles are read from the current preference, never frozen into a match or
// exported as another participant's personal data.
func profiles(ctx context.Context, tx *sql.Tx, v sessionRecord) (*[2]Profile, error) {
	result := &[2]Profile{{Kind: "deleted"}, {Kind: "deleted"}}
	for seat, participant := range v.Seats {
		if participant.User == nil {
			continue
		}
		var public bool
		var name, nick, id, avatar, guildAvatar string
		err := tx.QueryRowContext(ctx, `SELECT COALESCE(p.game_profile_public,u.game_profile_public),u.username,u.guild_nick,COALESCE(u.discord_id,''),u.avatar,u.guild_avatar_url FROM users u LEFT JOIN game_user_preferences p ON p.user_id=u.id WHERE u.id=?`, *participant.User).Scan(&public, &name, &nick, &id, &avatar, &guildAvatar)
		if err != nil {
			return nil, err
		}
		result[seat] = Profile{Kind: "anonymous"}
		if !public {
			continue
		}
		if nick != "" {
			name = nick
		}
		if !utf8.ValidString(name) {
			continue
		}
		name = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, strings.TrimSpace(name))
		runes := []rune(name)
		if len(runes) > 128 {
			runes = runes[:128]
		}
		if len(runes) == 0 {
			continue
		}
		profile := Profile{Kind: "public", DisplayName: string(runes)}
		parsed, err := url.Parse(guildAvatar)
		if err == nil && len(guildAvatar) <= 2048 && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil {
			profile.AvatarURL = &guildAvatar
		} else if profileAtom(id) && profileAtom(avatar) {
			candidate := "https://cdn.discordapp.com/avatars/" + id + "/" + avatar + ".png"
			profile.AvatarURL = &candidate
		}
		result[seat] = profile
	}
	return result, nil
}
func profileAtom(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
