// Package donationquota owns recurring charity counters inside the caller's
// existing transaction. It never opens a transaction or grants authorization.
package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const MaxRules = 16
const MaxRows = 5_000_000
const CleanupBatch = 1_000

var (
	ErrInvalid   = errors.New("donation quota: invalid rule")
	ErrConflict  = errors.New("donation quota: conflicting state")
	ErrLimited   = errors.New("donation quota: recurring limit reached")
	ErrCapacity  = errors.New("donation quota: storage capacity unavailable")
	ErrInvariant = errors.New("donation quota: inconsistent state")
)

type Reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type RuleInput struct {
	ID           *string `json:"id"`
	Mode         string  `json:"mode"`
	Interval     string  `json:"interval"`
	Alignment    *string `json:"alignment"`
	TimeZone     string  `json:"time_zone"`
	WeekStartsOn *int    `json:"week_starts_on"`
	Metric       string  `json:"metric"`
	Limit        string  `json:"limit"`
}

type RuleView struct {
	RuleInput
	Used             string `json:"used"`
	Reserved         string `json:"reserved"`
	Remaining        string `json:"remaining"`
	State            string `json:"state"`
	PeriodStart      *int64 `json:"period_start"`
	PeriodEnd        *int64 `json:"period_end"`
	NextTransitionAt *int64 `json:"next_transition_at"`
}

type epoch struct {
	id                        string
	number                    int64
	keyID                     int64
	order                     int
	rule                      RuleInput
	limit                     db.U128
	effective                 int64
	retired, observed, period sql.NullInt64
	left, at                  sql.NullInt64
	used, reserved, pending   db.U128
}

func Validate(input RuleInput) error {
	if input.ID != nil && !db.ValidateOpaqueID(*input.ID, "qlr_") {
		return ErrInvalid
	}
	if !calendar.ValidZone(input.TimeZone) {
		return ErrInvalid
	}
	switch input.Interval {
	case "1h", "5h", "day", "week", "month":
	default:
		return ErrInvalid
	}
	switch input.Mode {
	case "sliding":
		if input.Alignment != nil || input.WeekStartsOn != nil {
			return ErrInvalid
		}
	case "reset":
		if input.Alignment == nil {
			return ErrInvalid
		}
		switch *input.Alignment {
		case "first_success":
			if input.WeekStartsOn != nil {
				return ErrInvalid
			}
		case "calendar":
			if input.Interval == "1h" || input.Interval == "5h" {
				return ErrInvalid
			}
			if input.Interval == "week" {
				if input.WeekStartsOn == nil || *input.WeekStartsOn < 1 || *input.WeekStartsOn > 7 {
					return ErrInvalid
				}
			} else if input.WeekStartsOn != nil {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	_, err := parseMagnitude(input.Metric, input.Limit)
	return err
}

func parseMagnitude(metric, text string) (db.U128, error) {
	if len(text) == 0 || len(text) > 43 {
		return db.U128{}, ErrInvalid
	}
	if metric == "calls" || metric == "tokens" {
		n, err := db.ParseU128Decimal(text)
		if err != nil {
			return db.U128{}, ErrInvalid
		}
		return n, nil
	}
	if metric != "credits" {
		return db.U128{}, ErrInvalid
	}
	whole, fraction, point := strings.Cut(text, ".")
	w, err := db.ParseU128Decimal(whole)
	if err != nil || (point && (len(fraction) < 1 || len(fraction) > 3 || fraction[len(fraction)-1] == '0')) {
		return db.U128{}, ErrInvalid
	}
	var f int64
	for _, c := range fraction {
		if c < '0' || c > '9' {
			return db.U128{}, ErrInvalid
		}
		f = f*10 + int64(c-'0')
	}
	for i := len(fraction); i < 3; i++ {
		f *= 10
	}
	n := new(big.Int).Add(new(big.Int).Mul(w.Big(), big.NewInt(1000)), big.NewInt(f))
	result, err := db.U128FromBig(n)
	if err != nil {
		return db.U128{}, ErrInvalid
	}
	return result, nil
}

func formatMagnitude(metric string, value db.U128) string {
	if metric != "credits" {
		return value.Decimal()
	}
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(value.Big(), big.NewInt(1000), fraction)
	if fraction.Sign() == 0 {
		return whole.String()
	}
	return whole.String() + "." + strings.TrimRight(fmt.Sprintf("%03d", fraction.Int64()), "0")
}

func magnitude(blob []byte) (db.U128, error) {
	n, err := db.DecodeU128(blob)
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return n, nil
}

func add(a, b db.U128) (db.U128, error) {
	n, err := db.U128FromBig(new(big.Int).Add(a.Big(), b.Big()))
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return n, nil
}

func subtract(a, b db.U128) (db.U128, error) {
	n, err := db.U128FromBig(new(big.Int).Sub(a.Big(), b.Big()))
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return n, nil
}

func oneRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvariant
	}
	return nil
}

func validNow(now int64) bool { return now >= 0 && now <= calendar.MaxInstant }

func sameStructure(a, b RuleInput) bool {
	return a.Mode == b.Mode && a.Interval == b.Interval && a.TimeZone == b.TimeZone && a.Metric == b.Metric &&
		sameOptional(a.Alignment, b.Alignment) && sameOptional(a.WeekStartsOn, b.WeekStartsOn)
}

func sameOptional[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
