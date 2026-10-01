package lifecycle

import (
	"context"
	"database/sql"
)

// PersonalAutomationBatchExport retains only the owner's batch outcomes.
// Input secrets and internal replay material stay within their domain.
type PersonalAutomationBatchExport struct {
	ID        string                         `json:"id"`
	Kind      string                         `json:"kind"`
	TargetID  string                         `json:"target_id"`
	ItemCount int                            `json:"item_count"`
	CreatedAt int64                          `json:"created_at"`
	ExpiresAt int64                          `json:"expires_at"`
	Results   []PersonalAutomationStepExport `json:"results"`
}

type PersonalAutomationStepExport struct {
	Index         int    `json:"index"`
	Status        string `json:"status"`
	Outcome       string `json:"outcome,omitempty"`
	EndpointKeyID string `json:"endpoint_key_id,omitempty"`
	BindingID     string `json:"binding_id,omitempty"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
}

type PersonalAutomationExporter interface {
	ExportPersonalAutomation(context.Context, *sql.Tx, ExportRequest) ([]PersonalAutomationBatchExport, error)
}
