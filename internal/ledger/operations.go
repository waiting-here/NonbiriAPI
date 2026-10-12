package ledger

import "slices"

// operationRule is the single registry for operation/source relationships and
// audit dimensions. Adding an operation does not rebuild stored ledger facts.
type operationRule struct {
	source           sourceType
	channel          string
	behavior         string
	reason           bool
	donation         bool
	positiveSequence bool
	duel             bool
}

var operationRules = map[Kind]operationRule{
	KindRewardGrant:          {source: sourceOperation, channel: "admin", behavior: "adjustment", reason: true},
	KindAdminUserAdjustment:  {source: sourceOperation, channel: "admin", behavior: "adjustment", reason: true, donation: true},
	KindAdminPoolAdjustment:  {source: sourceOperation, channel: "admin", behavior: "adjustment", reason: true},
	KindAccountDeleteZero:    {source: sourceOperation, channel: "account", behavior: "deletion"},
	KindCheckinAward:         {source: sourceOperation, channel: "checkin", behavior: "award"},
	KindAntiAbusePenalty:     {source: sourceOperation, channel: "penalty", behavior: "penalty", reason: true},
	KindWelfareClaim:         {source: sourceOperation, channel: "welfare", behavior: "payout"},
	KindThursdayContribution: {source: sourceOperation, channel: "thursday", behavior: "contribution"},
	KindThursdayPayout:       {source: sourceOperation, channel: "thursday", behavior: "payout"},
	KindThursdayFinalize:     {source: sourcePeriod, channel: "thursday", behavior: "transfer"},
	KindForwardReserve:       {source: sourceLogicalRequest, channel: "api", behavior: "reserve"},
	KindForwardSettle:        {source: sourceLogicalRequest, channel: "api", behavior: "settlement"},
	KindForwardRelease:       {source: sourceLogicalRequest, channel: "api", behavior: "refund"},
	KindCharityReserve:       {source: sourceLogicalRequest, channel: "charity", behavior: "reserve"},
	KindCharitySettle:        {source: sourceLogicalRequest, channel: "charity", behavior: "settlement"},
	KindCharityRelease:       {source: sourceLogicalRequest, channel: "charity", behavior: "refund"},
	KindDonorReward:          {source: sourceDispatchClaim, channel: "donation", behavior: "reward", donation: true},
	KindFishingReserve:       {source: sourceFishingBatch, channel: "fishing", behavior: "reserve"},
	KindFishingSettle:        {source: sourceFishingBatch, channel: "fishing", behavior: "settlement"},
	KindFishingRelease:       {source: sourceFishingBatch, channel: "fishing", behavior: "refund"},
	KindLinkLinkEntry:        {source: sourceLinkLinkSession, channel: "linklink", behavior: "fee"},
	KindRPSQueueReserve:      {source: sourceRPSQueue, channel: "rps", behavior: "reserve"},
	KindRPSQueueRelease:      {source: sourceRPSQueue, channel: "rps", behavior: "refund"},
	KindRPSSessionStart:      {source: sourceRPSSession, channel: "rps", behavior: "transfer"},
	KindRPSRoundCut:          {source: sourceRPSSession, channel: "rps", behavior: "fee", positiveSequence: true},
	KindRPSTerminal:          {source: sourceRPSSession, channel: "rps", behavior: "settlement"},
	KindDuelQueueReserve:     {source: sourceDuelQueue, behavior: "reserve", duel: true},
	KindDuelQueueRelease:     {source: sourceDuelQueue, behavior: "refund", duel: true},
	KindDuelSessionStart:     {source: sourceDuelSession, behavior: "transfer", duel: true},
	KindDuelTerminal:         {source: sourceDuelSession, behavior: "settlement", duel: true},
	KindAITicket:             {source: sourceDuelSession, behavior: "reserve", duel: true},
	KindAITerminal:           {source: sourceDuelSession, behavior: "settlement", duel: true},
	KindBlackjackReserve:     {source: sourceBlackjackPayment, channel: "blackjack", behavior: "reserve"},
	KindBlackjackSettle:      {source: sourceBlackjackPayment, channel: "blackjack", behavior: "settlement"},
	KindBlackjackRelease:     {source: sourceBlackjackPayment, channel: "blackjack", behavior: "refund"},
	KindCatchTicket:          {source: sourceCatchSession, channel: "steadycatch", behavior: "fee"},
	KindCatchRefund:          {source: sourceCatchSession, channel: "steadycatch", behavior: "refund"},
	KindCatchReward:          {source: sourceCatchSession, channel: "steadycatch", behavior: "reward"},
	KindGameOnboardingReward: {source: sourceOperation, channel: "onboarding", behavior: "reward"},
	KindActivityLoan:         {source: sourceOperation, channel: "loan", behavior: "exchange"},
	KindActivityExchange:     {source: sourceOperation, channel: "picture_book", behavior: "exchange"},
	KindImageReserve:         {source: sourceImageTask, channel: "picture_book", behavior: "reserve"},
	KindImageSettle:          {source: sourceImageTask, channel: "picture_book", behavior: "settlement"},
	KindImageRefund:          {source: sourceImageTask, channel: "picture_book", behavior: "refund"},
	KindImageDeleteFinalize:  {source: sourceImageTask, channel: "picture_book", behavior: "deletion"},
	KindInactivityDecay:      {source: sourceOperation, channel: "inactivity", behavior: "decay"},
	KindFatFishUnlock:        {source: sourceOperation, channel: "fat_fish", behavior: "fee"},
	KindFatFishTicket:        {source: sourceOperation, channel: "fat_fish", behavior: "fee"},
	KindFatFishReward:        {source: sourceOperation, channel: "fat_fish", behavior: "reward"},
	KindFatFishRefund:        {source: sourceOperation, channel: "fat_fish", behavior: "refund"},
	KindLakeEntry:            {source: sourceOperation, channel: "lake_notes", behavior: "fee"},
	KindLakeExchange:         {source: sourceOperation, channel: "lake_notes", behavior: "exchange"},
	KindLakeCollectionReward: {source: sourceOperation, channel: "lake_notes", behavior: "reward", donation: true},
	KindGachaTicketPurchase:  {source: sourceOperation, channel: "gacha", behavior: "exchange"},
	KindGachaThemeReward:     {source: sourceOperation, channel: "gacha", behavior: "reward", donation: true},
}

func sourceTypeForKind(kind Kind) (sourceType, bool) {
	rule, ok := operationRules[kind]
	return rule.source, ok
}

// RegisteredKinds returns a stable copy of the supported operation identities.
func RegisteredKinds() []Kind {
	kinds := make([]Kind, 0, len(operationRules))
	for kind := range operationRules {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}
