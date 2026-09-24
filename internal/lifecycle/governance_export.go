package lifecycle

import (
	"context"
	"database/sql"
)

type GovernanceExporter interface {
	ExportGovernance(context.Context, *sql.Tx, ExportRequest) (GovernanceExport, error)
}

// These explicit export fields exclude prompts, execution parameters, upstream
// identities, source headers and generated images from personal downloads.
type GovernanceExport struct {
	LimitedActivities LimitedActivityExport `json:"limited_activities"`
	ImageTasks        []ImageTaskExport     `json:"image_tasks"`
	Inactivity        InactivityExport      `json:"inactivity"`
}
type ActivityWalletExport struct {
	General string `json:"general"`
	Paper   string `json:"sketch_paper"`
	Brush   string `json:"sketch_brush"`
}
type LimitedActivityExport struct {
	Wallet    ActivityWalletExport     `json:"wallet"`
	Exchanges []ActivityExchangeExport `json:"exchanges"`
}
type ActivityExchangeExport struct {
	OperationID    string `json:"operation_id"`
	ActivityKey    string `json:"activity_key"`
	ConfigRevision string `json:"config_revision"`
	Asset          string `json:"asset"`
	Quantity       string `json:"quantity"`
	UnitPrice      string `json:"unit_price"`
	Cost           string `json:"cost"`
	LedgerSeq      string `json:"ledger_seq"`
	CreatedAt      int64  `json:"created_at"`
}
type ImagePriceExport struct {
	Paper string `json:"paper"`
	Brush string `json:"brush"`
}
type ImageTaskExport struct {
	ID           string           `json:"id"`
	Status       string           `json:"status"`
	N            int              `json:"n"`
	CreatedAt    int64            `json:"created_at"`
	DispatchedAt *int64           `json:"dispatched_at"`
	CompletedAt  *int64           `json:"completed_at"`
	BillingState string           `json:"billing_state"`
	Charge       ImagePriceExport `json:"charge"`
	Refund       ImagePriceExport `json:"refund"`
	ActualImages int              `json:"actual_images"`
}
type ActiveStateExport struct {
	ObservationStartedAt int64  `json:"observation_started_at"`
	LastActiveAt         *int64 `json:"last_active_at"`
	ActivitySeq          int64  `json:"activity_seq,string"`
	ActivityEpoch        int64  `json:"activity_epoch,string"`
	LastDecayAt          *int64 `json:"last_decay_at"`
}
type InactivityExport struct {
	Activity *ActiveStateExport    `json:"activity"`
	Runs     []InactivityRunExport `json:"runs"`
}
type InactivityRunExport struct {
	ID             string  `json:"id"`
	UserID         *string `json:"user_id"`
	PolicyRevision int64   `json:"policy_revision,string"`
	ActivityEpoch  int64   `json:"activity_epoch,string"`
	DueSlot        int64   `json:"due_slot"`
	Action         string  `json:"action"`
	GeneralMilli   string  `json:"general_milli"`
	GameMilli      string  `json:"game_milli"`
	OperationID    *string `json:"ledger_operation_id"`
	CreatedAt      int64   `json:"created_at"`
}
