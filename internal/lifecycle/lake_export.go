package lifecycle

import "github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"

// Lake exports contain personal progress and historical financial receipts.
type LakeNotesExport struct {
	RulesID         string               `json:"rules_id"`
	ProfileRevision string               `json:"profile_revision"`
	Profile         rules.Profile        `json:"profile"`
	Casts           []LakeCastExport     `json:"casts"`
	Entries         []LakeEntryExport    `json:"entries"`
	Exchanges       []LakeExchangeExport `json:"exchanges"`
}
type LakeCastExport struct {
	ID             string     `json:"id"`
	SourcePeriodID string     `json:"source_period_id"`
	RulesID        string     `json:"rules_id"`
	Generation     string     `json:"generation"`
	Revision       string     `json:"revision"`
	AckTick        uint64     `json:"ack_tick"`
	Phase          string     `json:"phase"`
	Paused         bool       `json:"paused"`
	State          rules.Cast `json:"state"`
}
type LakeEntryExport struct {
	PeriodID       string `json:"period_id"`
	PeriodRevision string `json:"period_revision"`
	FeeMilli       string `json:"fee_milli"`
	OperationID    string `json:"operation_id,omitempty"`
	LedgerSeq      string `json:"ledger_seq,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}
type LakeExchangeExport struct {
	ID             string `json:"id"`
	Direction      string `json:"direction"`
	Quantity       string `json:"quantity"`
	PeriodID       string `json:"period_id"`
	PeriodRevision string `json:"period_revision"`
	SourceAmount   string `json:"source_amount"`
	TargetAmount   string `json:"target_amount"`
	SourceLot      string `json:"source_lot"`
	TargetLot      string `json:"target_lot"`
	OperationID    string `json:"operation_id"`
	LedgerSeq      string `json:"ledger_seq"`
	CreatedAt      int64  `json:"created_at"`
}
