package adminapi

// Wire response shapes for the admin user-management routes. Timestamps are
// unix seconds like the other projections. No shape carries a caller-key
// plaintext, session material, upstream secret, ciphertext, or request
// content: the projection is built from the repository's safe user row only.

import (
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// userResp is one bounded admin user row: identity, ban metadata, per-user
// limits, and the server-authoritative usage totals. endpoint_limit /
// rpm_limit are null when the stored value is NULL (global default applies).
type userResp struct {
	ID                        int64  `json:"id"`
	Username                  string `json:"username"`
	Avatar                    string `json:"avatar"`
	DiscordID                 string `json:"discord_id"`
	IsBanned                  bool   `json:"is_banned"`
	BannedReason              string `json:"banned_reason"`
	BannedUntil               *int64 `json:"banned_until"`
	AutoBanned                bool   `json:"auto_banned"`
	CharitySuspendedUntil     *int64 `json:"charity_suspended_until"`
	EndpointLimit             *int   `json:"endpoint_limit"`
	RPMLimit                  *int   `json:"rpm_limit"`
	Lang                      string `json:"lang"`
	TotalRequests             int64  `json:"total_requests"`
	TotalPromptTokens         int64  `json:"total_prompt_tokens"`
	TotalCompletionTokens     int64  `json:"total_completion_tokens"`
	TotalUnknownUsageRequests int64  `json:"total_unknown_usage_requests"`
	// Economic balances are canonical decimal strings (never JSON numbers):
	// credits_balance is the signed consumption balance, the
	// donation_credit_balance name keeps a delta adjustment response from
	// being confused with the resulting balance.
	CreditsBalance        string `json:"credits_balance"`
	DonationCreditBalance string `json:"donation_credit_balance"`
	CreatedAt             int64  `json:"created_at"`
}

// unixSecondsPtr projects a nullable deadline as a JSON number pointer.
func unixSecondsPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// userListResp is one page of users plus the explicit has_more flag, so the
// client never infers pagination from a raw page size.
type userListResp struct {
	Data    []userResp `json:"data"`
	HasMore bool       `json:"has_more"`
}

func userResponse(u *db.User) userResp {
	if u == nil {
		return userResp{}
	}
	return userResp{
		ID:                        u.ID,
		Username:                  u.Username,
		Avatar:                    u.Avatar,
		DiscordID:                 u.DiscordID,
		IsBanned:                  u.IsBanned,
		BannedReason:              u.BannedReason,
		BannedUntil:               unixSecondsPtr(u.BannedUntil),
		AutoBanned:                u.AutoBanned,
		CharitySuspendedUntil:     unixSecondsPtr(u.CharitySuspendedUntil),
		EndpointLimit:             u.EndpointLimit,
		RPMLimit:                  u.RPMLimit,
		Lang:                      u.Lang,
		TotalRequests:             u.TotalRequests,
		TotalPromptTokens:         u.TotalPromptTokens,
		TotalCompletionTokens:     u.TotalCompletionTokens,
		TotalUnknownUsageRequests: u.TotalUnknownUsageRequests,
		CreditsBalance:            credits.FormatAmount(u.Credits),
		DonationCreditBalance:     credits.FormatAmount(u.DonationCredit),
		CreatedAt:                 u.CreatedAt.Unix(),
	}
}

func userListResponse(users []db.User, hasMore bool) userListResp {
	out := userListResp{Data: make([]userResp, 0, len(users)), HasMore: hasMore}
	for i := range users {
		out.Data = append(out.Data, userResponse(&users[i]))
	}
	return out
}

// siteConfigPatchResp echoes the applied key and its typed value.
type siteConfigPatchResp struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}
