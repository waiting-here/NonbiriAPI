// Package rewards owns administrator reward deliveries and redemption codes.
package rewards

import (
	"context"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/limitedactivities"
	"github.com/waiting-here/NonbiriAPI/internal/rewardasset"
	"time"
)

var (
	ErrInvalid     = errors.New("rewards: invalid request")
	ErrConflict    = errors.New("rewards: conflict")
	ErrUnavailable = errors.New("rewards: unavailable")
	ErrConditions  = errors.New("rewards: conditions not met")
	ErrCapacity    = errors.New("rewards: capacity")
	ErrRate        = errors.New("rewards: rate limit")
	ErrExportLimit = errors.New("rewards: export limit")
)

const maxCount = int64(9007199254740991)

type UserAuthorizer interface {
	AuthorizeUserMutation(context.Context, *sql.Tx, int64) error
}
type AdminAuthorizer interface {
	AuthorizeAdmin(context.Context, *sql.Tx, int64) error
}
type Gate interface {
	AuthorizeUserActivity(context.Context, *sql.Tx, int64) error
}
type Config struct {
	Database *sql.DB
	Users    UserAuthorizer
	Admins   AdminAuthorizer
	Gate     Gate
	Activity limitedactivities.ActivityRecorder
	Now      func() time.Time
}
type Condition struct {
	Op       string      `json:"op"`
	Children []Condition `json:"children,omitempty"`
	Field    string      `json:"field,omitempty"`
	Asset    string      `json:"asset,omitempty"`
	Compare  string      `json:"compare,omitempty"`
	Value    string      `json:"value,omitempty"`
}
type Input struct {
	Kind       string             `json:"kind"`
	Title      string             `json:"title"`
	Rewards    []rewardasset.Item `json:"rewards"`
	Conditions Condition          `json:"conditions"`
	Code       string             `json:"code,omitempty"`
	Enabled    bool               `json:"enabled"`
	Quota      *int64             `json:"quota,omitempty"`
	StartsAt   *int64             `json:"starts_at,omitempty"`
	EndsAt     *int64             `json:"ends_at,omitempty"`
}
type Campaign struct {
	ID string `json:"id"`
	Input
	Revision  int64 `json:"revision"`
	Claimed   int64 `json:"claimed"`
	Matched   int64 `json:"matched"`
	Skipped   int64 `json:"skipped"`
	Pending   int64 `json:"pending"`
	Failed    int64 `json:"failed"`
	CreatedAt int64 `json:"created_at"`
}
type Edit struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Title    string `json:"title"`
	Enabled  bool   `json:"enabled"`
	Quota    *int64 `json:"quota,omitempty"`
	StartsAt *int64 `json:"starts_at,omitempty"`
	EndsAt   *int64 `json:"ends_at,omitempty"`
}
type Receipt struct {
	ID          int64                `json:"id"`
	CampaignID  string               `json:"campaign_id"`
	Title       string               `json:"title"`
	Kind        string               `json:"kind"`
	Status      string               `json:"status"`
	Results     []rewardasset.Result `json:"results"`
	CreatedAt   int64                `json:"created_at"`
	CompletedAt *int64               `json:"completed_at,omitempty"`
}
type Redemption struct {
	Receipt
	AlreadyClaimed bool `json:"already_claimed"`
}

type Preview struct {
	Matched int64 `json:"matched"`
	Banned  int64 `json:"banned"`
	At      int64 `json:"at"`
}
type CampaignPage struct {
	Items []Campaign `json:"items"`
	Next  string     `json:"next_cursor"`
}
type ReceiptPage struct {
	Items []Receipt `json:"items"`
	Next  string    `json:"next_cursor"`
}
