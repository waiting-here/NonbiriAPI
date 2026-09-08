package db

import (
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"modernc.org/sqlite"
)

var errBrowseSQLValue = errors.New("invalid browse scalar")

// These pure functions let authorized browse queries aggregate exact wide
// counters and calendar windows within their existing read snapshot. They
// never open a connection, reserve quota, or retain database argument buffers.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("nbi_u128", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		n, ok := args[0].(int64)
		if !ok || n < 0 {
			return nil, errBrowseSQLValue
		}
		value, err := U128FromBig(big.NewInt(n))
		return EncodeU128(value), err
	})
	sqlite.MustRegisterDeterministicScalarFunction("nbi_u128_remaining", 4, sqlWideRemaining)
	sqlite.MustRegisterFunction("nbi_u128_sum", &sqlite.FunctionImpl{
		NArgs: 1, Deterministic: true,
		MakeAggregate: func(sqlite.FunctionContext) (sqlite.AggregateFunction, error) { return &sqlWideSum{}, nil },
	})
	sqlite.MustRegisterDeterministicScalarFunction("nbi_calendar_subtract", 3, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		now, interval, zone, err := sqlCalendarArguments(args)
		if err != nil {
			return nil, err
		}
		return calendar.Subtract(now, interval, zone)
	})
	sqlite.MustRegisterDeterministicScalarFunction("nbi_calendar_start", 4, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		now, interval, zone, err := sqlCalendarArguments(args)
		week, ok := args[3].(int64)
		if err != nil || !ok || week < 0 || week > 7 {
			return nil, errBrowseSQLValue
		}
		period, err := calendar.NaturalPeriod(now, interval, zone, int(week))
		return period.Start, err
	})
	sqlite.MustRegisterDeterministicScalarFunction("nbi_donation_source", 3, sqlDonationSource)
}

func sqlWideValue(value driver.Value) (*big.Int, error) {
	blob, ok := value.([]byte)
	if !ok || len(blob) != 16 {
		return nil, errBrowseSQLValue
	}
	return new(big.Int).SetBytes(blob), nil
}

func sqlWideRemaining(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	limit := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	if args[0] != nil {
		var err error
		limit, err = sqlWideValue(args[0])
		if err != nil {
			return nil, err
		}
	}
	for _, argument := range args[1:] {
		value, err := sqlWideValue(argument)
		if err != nil {
			return nil, err
		}
		if args[0] != nil {
			limit.Sub(limit, value)
		}
	}
	if limit.Sign() < 0 {
		limit.SetInt64(0)
	}
	value, err := U128FromBig(limit)
	return EncodeU128(value), err
}

type sqlWideSum struct{ sum big.Int }

func (sum *sqlWideSum) Step(_ *sqlite.FunctionContext, args []driver.Value) error {
	value, err := sqlWideValue(args[0])
	if err != nil {
		return err
	}
	sum.sum.Add(&sum.sum, value)
	if sum.sum.BitLen() > 128 {
		return errBrowseSQLValue
	}
	return nil
}

func (sum *sqlWideSum) WindowInverse(_ *sqlite.FunctionContext, args []driver.Value) error {
	value, err := sqlWideValue(args[0])
	if err != nil {
		return err
	}
	sum.sum.Sub(&sum.sum, value)
	if sum.sum.Sign() < 0 {
		return errBrowseSQLValue
	}
	return nil
}

func (sum *sqlWideSum) WindowValue(*sqlite.FunctionContext) (driver.Value, error) {
	value, err := U128FromBig(&sum.sum)
	return EncodeU128(value), err
}

func (*sqlWideSum) Final(*sqlite.FunctionContext) {}

func sqlCalendarArguments(args []driver.Value) (int64, string, string, error) {
	now, nowOK := args[0].(int64)
	interval, intervalOK := args[1].(string)
	zone, zoneOK := args[2].(string)
	if !nowOK || !intervalOK || !zoneOK {
		return 0, "", "", errBrowseSQLValue
	}
	return now, interval, zone, nil
}

func sqlDonationSource(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	parts := []string{"donation-source-v1", "custom"}
	if args[0] != nil {
		channel, ok := args[0].(string)
		if !ok || channel == "" || len(channel) > 128 || !utf8.ValidString(channel) || strings.ContainsRune(channel, 0) {
			return nil, errBrowseSQLValue
		}
		parts = []string{"donation-source-v1", "mainstream", channel}
	} else {
		for _, argument := range args[1:] {
			value, ok := argument.(string)
			if !ok || value == "" || len(value) > 16384 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return nil, errBrowseSQLValue
			}
			parts = append(parts, value)
		}
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return nil, errBrowseSQLValue
	}
	digest := sha256.Sum256(encoded)
	return "dsg_" + base64.RawURLEncoding.EncodeToString(digest[:]), nil
}
