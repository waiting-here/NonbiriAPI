// Package agentplan defines the narrow contract between assistant workflows
// and existing site domains. It contains no model client or HTTP transport.
package agentplan

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Actor struct {
	ID                int64  `json:"id,string"`
	Role              string `json:"role"`
	Label             string `json:"label"`
	SessionHash       string `json:"-"`
	SessionGeneration string `json:"-"`
	Elevation         string `json:"-"`
	// Automatic is set only for the service-owned review worker. It is never
	// decoded from a web request or a model-produced operation.
	Automatic bool `json:"-"`
}

type Operation struct {
	Kind     string          `json:"kind"`
	TargetID string          `json:"target_id"`
	Input    json.RawMessage `json:"input"`
}

type Preview struct {
	Title             string `json:"title"`
	Description       string `json:"description"`
	Before            string `json:"before,omitempty"`
	After             string `json:"after,omitempty"`
	Dangerous         bool   `json:"dangerous"`
	RequiresElevation bool   `json:"requires_elevation"`
}

type Prepared interface {
	// Apply must not commit, perform network I/O or run post-commit callbacks.
	Apply(context.Context, *sql.Tx) (json.RawMessage, error)
	// Finalize runs exactly once after the outer commit or rollback.
	Finalize(committed bool)
}

// Authorize checks the real actor and current grants in the same transaction
// as the operation. The domain still owns target hierarchy, versions and rules.
type Authorize func(context.Context, *sql.Tx, Actor, string) error
