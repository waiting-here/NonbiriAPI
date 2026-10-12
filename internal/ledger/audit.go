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
	rule, known := operationRules[kind]
	channel, behavior := rule.channel, rule.behavior
	if rule.duel {
		if kind == KindAITicket || kind == KindAITerminal {
			channel = duelIDGame(sourceID, false)
		} else {
			switch {
			case strings.HasPrefix(sourceID, "bid_"), strings.HasPrefix(sourceID, "bidq_"):
				channel = "bidding"
			case strings.HasPrefix(sourceID, "lik_"), strings.HasPrefix(sourceID, "likq_"):
				channel = "likes"
			case strings.HasPrefix(sourceID, "gwt_"), strings.HasPrefix(sourceID, "gwtq_"):
				channel = "gwent"
			}
		}
	}
	if !known {
		channel = ""
	}
	if channel == "" {
		return AuditClassification{Channel: "unclassified", Behavior: "unclassified"}
	}
	return AuditClassification{Channel: channel, Behavior: behavior, Known: true}
}
