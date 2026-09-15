package lifecycle

import (
	"context"
	"database/sql"
)

type RandomnessStreamExport struct {
	Label   string `json:"label"`
	Samples string `json:"samples"`
}

type RandomnessProofExport struct {
	Algorithm  string                   `json:"algorithm"`
	Game       string                   `json:"game"`
	ResourceID string                   `json:"resource_id"`
	Rules      string                   `json:"rules"`
	Commitment string                   `json:"commitment"`
	Seed       string                   `json:"seed,omitempty"`
	Streams    []RandomnessStreamExport `json:"streams,omitempty"`
}

type RandomnessExporter interface {
	ExportRandomness(context.Context, *sql.Tx, ExportRequest) ([]RandomnessProofExport, error)
}
