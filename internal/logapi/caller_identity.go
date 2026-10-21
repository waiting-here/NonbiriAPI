package logapi

import (
	"database/sql"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

const charityRouteKindsSQL = "('charity_chat_completions','charity_embeddings','charity_images_generations','charity_decisions','charity_responses','charity_responses_compact','charity_responses_input_tokens','charity_completions','charity_images_edits','charity_images_variations','charity_audio_speech','charity_audio_transcriptions','charity_audio_translations','charity_moderations','charity_rerank','charity_systemone')"

type OriginIdentity struct {
	UserID          *string `json:"origin_user_id"`
	DiscordID       *string `json:"origin_discord_id"`
	Deleted         bool    `json:"origin_deleted"`
	Unknown         bool    `json:"origin_unknown"`
	HistoryRecordID *string `json:"history_record_id,omitempty"`
}

// OAuth stores the Discord display name in username, falling back to the
// username when that display name is absent. Guild nicknames take precedence.
// The join is limited to charity rows and surviving ordinary accounts; neither
// historical log snapshots nor donation owners participate in the identity.
// The requested charity model is projected separately from the log snapshot.
const callerIdentityColumns = `u.id IS NOT NULL,COALESCE(NULLIF(u.guild_nick,''),NULLIF(u.username,'')),NULLIF(u.discord_id,''),CASE WHEN l.route_kind IN ` + charityRouteKindsSQL + ` THEN NULLIF(l.model,'') END,l.origin_user_id,l.origin_discord_id,l.user_id IS NULL,(SELECT alert_id FROM admin_account_deletions WHERE former_user_id=l.origin_user_id),CASE WHEN l.rejection_stage IS NOT NULL THEN l.error_diag END,cm.id`
const callerIdentityJoin = ` LEFT JOIN users u ON u.id=l.user_id AND u.is_admin=0 AND l.route_kind IN ` + charityRouteKindsSQL + `
LEFT JOIN charity_reservations cr ON cr.logical_request_id=l.logical_request_id AND l.route_kind IN ` + charityRouteKindsSQL + `
LEFT JOIN charity_models cm ON cm.id=cr.charity_model_id `

func scanManagementCommon(scanner rowScanner, extra ...any) (commonLogRecord, *CallerIdentity, error) {
	var present bool
	var nickname, discordID, charityModel sql.NullString
	var originUser, history, charityModelID sql.NullInt64
	var originDiscord sql.NullString
	var deleted bool
	var detail sql.NullString
	targets := append([]any{&present, &nickname, &discordID, &charityModel, &originUser, &originDiscord, &deleted, &history, &detail, &charityModelID}, extra...)
	record, err := scanCommon(scanner, targets...)
	if err != nil {
		return commonLogRecord{}, nil, err
	}
	if !utf8Bound(charityModel.String, 512) {
		return commonLogRecord{}, nil, ErrInvariant
	}
	record.rejectionDetail = requestattempt.DecodeDetail(detail.String)
	record.charityModel = textPointer(charityModel)
	record.charityModelID = nullableDecimal(charityModelID)
	record.origin = OriginIdentity{UserID: nullableDecimal(originUser), DiscordID: textPointer(originDiscord), Deleted: deleted, Unknown: !originUser.Valid || !originDiscord.Valid}
	if history.Valid {
		value := strconv.FormatInt(history.Int64, 10)
		record.origin.HistoryRecordID = &value
	}
	if !present {
		return record, nil, nil
	}
	if !utf8Bound(nickname.String, 256) || !utf8Bound(discordID.String, 128) {
		return commonLogRecord{}, nil, ErrInvariant
	}
	return record, &CallerIdentity{DiscordNickname: textPointer(nickname), DiscordID: textPointer(discordID)}, nil
}

// Match the call-time platform name; instr treats SQL wildcard characters literally.
const charityModelPredicate = " AND l.route_kind IN " + charityRouteKindsSQL + " AND instr(lower(l.model),lower(?))>0"
