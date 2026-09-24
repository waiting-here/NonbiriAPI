// Package economyaudit projects immutable monetary entries into non-personal
// aggregates and exposes consistent administrator-only accounting snapshots.
package economyaudit

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

var (
	ErrInvalid     = errors.New("invalid economy audit query")
	ErrInvariant   = errors.New("invalid economy audit state")
	ErrUnavailable = errors.New("economy audit is unavailable")
)

const maxUnix = int64(253402300799)
const batchSize = 100

type FinalAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}
type CursorKeyDeriver interface{ DeriveGenerationTwoSubkey([]byte) ([]byte, error) }
type AdminPrincipal struct{ UserID int64 }
type AuthorizedAdminHandler func(http.ResponseWriter, *http.Request, AdminPrincipal)
type AdminRouteRegistrar interface {
	RegisterAdminRoute(string, string, AuthorizedAdminHandler) error
}

type Config struct {
	Database   *sql.DB
	FinalAuth  FinalAuthorizer
	CursorKeys CursorKeyDeriver
	Now        func() time.Time
}

type Service struct {
	database *sql.DB
	auth     FinalAuthorizer
	keys     CursorKeyDeriver
	now      func() time.Time
}

type Filter struct {
	Asset                         ledger.Asset
	From, To                      int64
	Bucket, Kind, Channel, Cursor string
}

type Metrics struct {
	Issued           string `json:"issued"`
	Reclaimed        string `json:"reclaimed"`
	UserIncome       string `json:"user_income"`
	UserExpense      string `json:"user_expense"`
	InternalTransfer string `json:"internal_transfer"`
	Operations       string `json:"operations"`
}

type Coverage struct {
	Status                 string  `json:"status"`
	FirstLedgerSeq         *string `json:"first_ledger_seq"`
	FirstOccurredAt        *int64  `json:"first_occurred_at"`
	UnclassifiedOperations string  `json:"unclassified_operations"`
	OpeningKnown           bool    `json:"opening_known"`
}

type Metadata struct {
	Asset         ledger.Asset `json:"asset"`
	From          int64        `json:"from"`
	To            int64        `json:"to"`
	Unit          string       `json:"unit"`
	Scale         string       `json:"scale"`
	OffsetMinutes int          `json:"offset_minutes"`
	LedgerSeq     string       `json:"ledger_seq"`
	ProjectedSeq  string       `json:"projected_seq"`
	SnapshotAt    int64        `json:"snapshot_at"`
	Coverage      Coverage     `json:"coverage"`
}

type Inventory struct {
	UserAvailable    string `json:"user_available"`
	Frozen           string `json:"frozen"`
	Pools            string `json:"pools"`
	Platform         string `json:"platform"`
	NegativeUsers    string `json:"negative_users"`
	NegativeFrozen   string `json:"negative_frozen"`
	NegativePools    string `json:"negative_pools"`
	NegativePlatform string `json:"negative_platform"`
	Net              string `json:"net"`
}

type Reconciliation struct {
	Status             string  `json:"status"`
	Scope              string  `json:"scope"`
	InventoryNet       *string `json:"inventory_net"`
	LedgerNet          *string `json:"ledger_net"`
	IntervalOpeningNet *string `json:"interval_opening_net"`
	IntervalClosingNet *string `json:"interval_closing_net"`
	IntervalNetChange  *string `json:"interval_net_change"`
}

type Summary struct {
	Metadata       Metadata       `json:"metadata"`
	Flows          Metrics        `json:"flows"`
	Inventory      *Inventory     `json:"inventory"`
	Reconciliation Reconciliation `json:"reconciliation"`
}

type Point struct {
	Start   int64   `json:"start"`
	End     int64   `json:"end"`
	Metrics Metrics `json:"metrics"`
}

type Series struct {
	Metadata Metadata `json:"metadata"`
	Bucket   string   `json:"bucket"`
	Data     []Point  `json:"data"`
}

type Channel struct {
	Kind       string  `json:"kind"`
	SourceType string  `json:"source_type"`
	Channel    string  `json:"channel"`
	Known      bool    `json:"known"`
	Metrics    Metrics `json:"metrics"`
}

type Channels struct {
	Metadata Metadata  `json:"metadata"`
	Data     []Channel `json:"data"`
}

type OperationEntry struct {
	Asset       ledger.Asset `json:"asset"`
	AccountKind string       `json:"account_kind"`
	UserID      *string      `json:"user_id"`
	Delta       string       `json:"delta"`
}

type Operation struct {
	ID             string                     `json:"id"`
	LedgerSeq      string                     `json:"ledger_seq"`
	Kind           string                     `json:"kind"`
	SourceType     string                     `json:"source_type"`
	SourceID       string                     `json:"source_id"`
	CreatedAt      int64                      `json:"created_at"`
	Classification ledger.AuditClassification `json:"classification"`
	Entries        []OperationEntry           `json:"entries"`
}

type Operations struct {
	Metadata   Metadata    `json:"metadata"`
	AnchorSeq  string      `json:"anchor_seq"`
	Data       []Operation `json:"data"`
	NextCursor *string     `json:"next_cursor"`
}
