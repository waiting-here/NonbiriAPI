package ledger

import "strings"

// AuditClassification labels the reason for a movement. Monetary issuance is
// always computed from external-account entries, never from these labels.
type AuditClassification struct {
	Channel  string `json:"channel"`
	Behavior string `json:"behavior"`
	Known    bool   `json:"known"`
}

// ClassifyForAudit is exhaustive for the closed operation set. A future or
// unknown kind remains visible as a coverage gap instead of being omitted.
func ClassifyForAudit(kind Kind, sourceID string) AuditClassification {
	channel, behavior := "", ""
	switch kind {
	case KindAdminUserAdjustment, KindAdminPoolAdjustment:
		channel, behavior = "admin", "adjustment"
	case KindAccountDeleteZero:
		channel, behavior = "account", "deletion"
	case KindCheckinAward:
		channel, behavior = "checkin", "award"
	case KindAntiAbusePenalty:
		channel, behavior = "penalty", "penalty"
	case KindWelfareClaim:
		channel, behavior = "welfare", "payout"
	case KindThursdayContribution:
		channel, behavior = "thursday", "contribution"
	case KindThursdayPayout:
		channel, behavior = "thursday", "payout"
	case KindThursdayFinalize:
		channel, behavior = "thursday", "transfer"
	case KindForwardReserve, KindForwardSettle, KindForwardRelease:
		channel, behavior = "api", movementBehavior(kind)
	case KindCharityReserve, KindCharitySettle, KindCharityRelease:
		channel, behavior = "charity", movementBehavior(kind)
	case KindDonorReward:
		channel, behavior = "donation", "reward"
	case KindFishingReserve, KindFishingSettle, KindFishingRelease:
		channel, behavior = "fishing", movementBehavior(kind)
	case KindLinkLinkEntry:
		channel, behavior = "linklink", "fee"
	case KindRPSQueueReserve, KindRPSQueueRelease, KindRPSSessionStart, KindRPSRoundCut, KindRPSTerminal:
		channel, behavior = "rps", movementBehavior(kind)
	case KindDuelQueueReserve, KindDuelQueueRelease, KindDuelSessionStart, KindDuelTerminal:
		if strings.HasPrefix(sourceID, "bid_") || strings.HasPrefix(sourceID, "bidq_") {
			channel = "bidding"
		} else if strings.HasPrefix(sourceID, "lik_") || strings.HasPrefix(sourceID, "likq_") {
			channel = "likes"
		}
		behavior = movementBehavior(kind)
	case KindBlackjackReserve, KindBlackjackSettle, KindBlackjackRelease:
		channel, behavior = "blackjack", movementBehavior(kind)
	case KindGameOnboardingReward:
		channel, behavior = "onboarding", "reward"
	case KindActivityLoan:
		channel, behavior = "loan", "exchange"
	case KindActivityExchange:
		channel, behavior = "picture_book", "exchange"
	case KindImageReserve, KindImageSettle, KindImageRefund, KindImageDeleteFinalize:
		channel, behavior = "picture_book", movementBehavior(kind)
	case KindInactivityDecay:
		channel, behavior = "inactivity", "decay"
	}
	if channel == "" {
		return AuditClassification{Channel: "unclassified", Behavior: "unclassified"}
	}
	return AuditClassification{Channel: channel, Behavior: behavior, Known: true}
}

func movementBehavior(kind Kind) string {
	value := string(kind)
	switch {
	case strings.HasSuffix(value, "_reserve"):
		return "reserve"
	case strings.HasSuffix(value, "_release"), strings.HasSuffix(value, "_refund"):
		return "refund"
	case kind == KindImageDeleteFinalize:
		return "deletion"
	case strings.HasSuffix(value, "_start"):
		return "transfer"
	case strings.HasSuffix(value, "_cut"):
		return "fee"
	default:
		return "settlement"
	}
}
