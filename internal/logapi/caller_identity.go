package logapi

import "database/sql"

// OAuth stores the Discord display name in username, falling back to the
// username when that display name is absent. Guild nicknames take precedence.
// The join is limited to charity rows and surviving ordinary accounts; neither
// historical log snapshots nor donation owners participate in the identity.
// The requested charity model is projected separately from the log snapshot.
const callerIdentityColumns = `u.id IS NOT NULL,COALESCE(NULLIF(u.guild_nick,''),NULLIF(u.username,'')),NULLIF(u.discord_id,''),CASE WHEN l.route_kind IN ('charity_chat_completions','charity_embeddings') THEN NULLIF(l.model,'') END`
const callerIdentityJoin = ` LEFT JOIN users u ON u.id=l.user_id AND u.is_admin=0 AND l.route_kind IN ('charity_chat_completions','charity_embeddings') `

func scanManagementCommon(scanner rowScanner, extra ...any) (commonLogRecord, *CallerIdentity, error) {
	var present bool
	var nickname, discordID, charityModel sql.NullString
	targets := append([]any{&present, &nickname, &discordID, &charityModel}, extra...)
	record, err := scanCommon(scanner, targets...)
	if err != nil {
		return commonLogRecord{}, nil, err
	}
	if !utf8Bound(charityModel.String, 512) {
		return commonLogRecord{}, nil, ErrInvariant
	}
	record.charityModel = textPointer(charityModel)
	if !present {
		return record, nil, nil
	}
	if !utf8Bound(nickname.String, 256) || !utf8Bound(discordID.String, 128) {
		return commonLogRecord{}, nil, ErrInvariant
	}
	return record, &CallerIdentity{DiscordNickname: textPointer(nickname), DiscordID: textPointer(discordID)}, nil
}
