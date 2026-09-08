package logapi

import "database/sql"

// OAuth stores the Discord display name in username, falling back to the
// username when that display name is absent. Guild nicknames take precedence.
// The join is limited to charity rows and surviving ordinary accounts; neither
// historical log snapshots nor donation owners participate in this projection.
const callerIdentityColumns = `u.id IS NOT NULL,COALESCE(NULLIF(u.guild_nick,''),NULLIF(u.username,'')),NULLIF(u.discord_id,'')`
const callerIdentityJoin = ` LEFT JOIN users u ON u.id=l.user_id AND u.is_admin=0 AND l.route_kind='charity_chat_completions' `

func scanStewardCommon(scanner rowScanner) (commonLogRecord, *CallerIdentity, error) {
	var present bool
	var nickname, discordID sql.NullString
	record, err := scanCommon(scanner, &present, &nickname, &discordID)
	if err != nil {
		return commonLogRecord{}, nil, err
	}
	if !present {
		return record, nil, nil
	}
	if !utf8Bound(nickname.String, 256) || !utf8Bound(discordID.String, 128) {
		return commonLogRecord{}, nil, ErrInvariant
	}
	return record, &CallerIdentity{DiscordNickname: textPointer(nickname), DiscordID: textPointer(discordID)}, nil
}
