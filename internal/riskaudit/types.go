// Package riskaudit provides bounded evidence for human review. It never
// changes account balances, penalties, or access decisions.
package riskaudit

import (
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid audit request")
	ErrForbidden   = errors.New("audit access forbidden")
	ErrNotFound    = errors.New("audit record not found")
	ErrConflict    = errors.New("audit revision conflict")
	ErrUnavailable = errors.New("audit unavailable")
)

const (
	MaxPage   = 100
	MaxRules  = 1000
	Retention = 30 * 24 * time.Hour
)
const (
	CoveragePartial = 1 << iota
	CoverageDropped
	CoverageLimitChanged
	CoverageUnresolved
	CoverageClock
)

type Config struct {
	ThresholdPercent   int   `json:"threshold_percent"`
	ConsecutiveMinutes int   `json:"consecutive_minutes"`
	SharedIPHours      int   `json:"shared_ip_hours"`
	SharedIPUsers      int   `json:"shared_ip_users"`
	Revision           int64 `json:"revision"`
	UpdatedAt          int64 `json:"updated_at"`
}

func DefaultConfig() Config {
	return Config{ThresholdPercent: 80, ConsecutiveMinutes: 5, SharedIPHours: 24, SharedIPUsers: 3, Revision: 1}
}
func (c Config) Valid() bool {
	return c.ThresholdPercent >= 1 && c.ThresholdPercent <= 100 && c.ConsecutiveMinutes >= 1 && c.ConsecutiveMinutes <= 60 && c.SharedIPHours >= 1 && c.SharedIPHours <= 720 && c.SharedIPUsers >= 2 && c.SharedIPUsers <= 1000 && c.Revision >= 1
}

type Minute struct {
	Epoch             string `json:"epoch"`
	UserID            int64  `json:"user_id,string"`
	Minute            int64  `json:"minute"`
	Kind              string `json:"call_kind"`
	RPMCommitted      int64  `json:"rpm_committed"`
	RPMReleased       int64  `json:"rpm_released"`
	RPMPending        int64  `json:"rpm_pending"`
	RPMDenied         int64  `json:"rpm_denied"`
	ConcurrencyDenied int64  `json:"concurrency_denied"`
	OccupancyMillis   int64  `json:"occupancy_millis"`
	Peak              int    `json:"peak"`
	RPMLimit          int    `json:"rpm_limit"`
	ConcurrencyLimit  int    `json:"concurrency_limit"`
	Coverage          int    `json:"coverage"`
	ConfigRevision    int64  `json:"config_revision"`
	UpdatedAt         int64  `json:"updated_at"`
}
type Gap struct {
	Epoch     string `json:"epoch"`
	Minute    int64  `json:"minute"`
	Reason    string `json:"reason"`
	LostCount int64  `json:"lost_count"`
}
type Actor struct {
	Admin  bool
	UserID int64
}
type Window struct {
	From, To int64
	Limit    int
	After    int64
	Kind     string
	Model    string
}

func (w Window) validate(now int64) (Window, error) {
	if w.To == 0 {
		w.To = now
	}
	if w.From == 0 {
		w.From = w.To - 86400
	}
	if w.Limit == 0 {
		w.Limit = 50
	}
	if w.From < 0 || w.To <= w.From || w.To > now || w.To-w.From > int64(Retention/time.Second) || w.Limit < 1 || w.Limit > MaxPage || w.After < 0 {
		return Window{}, ErrInvalid
	}
	if w.Kind != "" && w.Kind != "total" && w.Kind != "self" && w.Kind != "charity" && w.Kind != "unclassified" {
		return Window{}, ErrInvalid
	}
	if len(w.Model) > 512 {
		return Window{}, ErrInvalid
	}
	return w, nil
}

type Page[T any] struct {
	Items    []T    `json:"items"`
	Next     int64  `json:"next,omitempty,string"`
	HasMore  bool   `json:"has_more"`
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Coverage string `json:"coverage"`
	Scanned  int    `json:"scanned,omitempty"`
}

func minuteAt(t time.Time) int64 { return t.UTC().Unix() / 60 * 60 }
func validKind(kind string) bool {
	return kind == "self" || kind == "charity" || kind == "unclassified"
}
