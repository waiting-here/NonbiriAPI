// Package charitydiscount evaluates fixed and weekly charity price reductions
// from an explicit decision instant and the pinned time-zone registry.
package charitydiscount

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

const (
	MaxPeriods  = 16
	MaxBytes    = 4096
	daySeconds  = 24 * 60 * 60
	weekSeconds = 7 * daySeconds
)

var ErrInvalid = errors.New("charity discount: invalid configuration or instant")

type Period struct {
	Weekdays  []int  `json:"weekdays"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Percent   int    `json:"percent"`
}

type Config struct {
	Enabled        bool     `json:"enabled"`
	TimeZone       string   `json:"time_zone"`
	DefaultPercent int      `json:"default_percent"`
	Periods        []Period `json:"periods"`
}

func Default() Config {
	return Config{TimeZone: "UTC", DefaultPercent: 100, Periods: []Period{}}
}

func (value Config) Clone() Config {
	value.Periods = slices.Clone(value.Periods)
	for index := range value.Periods {
		value.Periods[index].Weekdays = slices.Clone(value.Periods[index].Weekdays)
	}
	return value
}

// UnmarshalJSON requires a complete replacement, including disabled schedules.
// Normalization makes semantically identical weekday orders share one encoding.
func (value *Config) UnmarshalJSON(data []byte) error {
	if value == nil || len(data) > MaxBytes || strictjson.ValidateObject(data) != nil ||
		!exactFields(data, "enabled", "time_zone", "default_percent", "periods") {
		return ErrInvalid
	}
	var wire struct {
		Enabled        *bool              `json:"enabled"`
		TimeZone       *string            `json:"time_zone"`
		DefaultPercent *int               `json:"default_percent"`
		Periods        *[]json.RawMessage `json:"periods"`
	}
	if json.Unmarshal(data, &wire) != nil || wire.Enabled == nil || wire.TimeZone == nil ||
		wire.DefaultPercent == nil || wire.Periods == nil || len(*wire.Periods) > MaxPeriods {
		return ErrInvalid
	}
	out := Config{Enabled: *wire.Enabled, TimeZone: *wire.TimeZone, DefaultPercent: *wire.DefaultPercent, Periods: make([]Period, 0, len(*wire.Periods))}
	for _, encoded := range *wire.Periods {
		if !exactFields(encoded, "weekdays", "start_time", "end_time", "percent") {
			return ErrInvalid
		}
		var period struct {
			Weekdays  *[]int  `json:"weekdays"`
			StartTime *string `json:"start_time"`
			EndTime   *string `json:"end_time"`
			Percent   *int    `json:"percent"`
		}
		if json.Unmarshal(encoded, &period) != nil || period.Weekdays == nil ||
			period.StartTime == nil || period.EndTime == nil || period.Percent == nil {
			return ErrInvalid
		}
		out.Periods = append(out.Periods, Period{Weekdays: *period.Weekdays, StartTime: *period.StartTime, EndTime: *period.EndTime, Percent: *period.Percent})
	}
	normalized, err := Normalize(out)
	if err != nil {
		return err
	}
	*value = normalized
	return nil
}

func exactFields(data []byte, fields ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || len(object) != len(fields) {
		return false
	}
	for _, field := range fields {
		if _, found := object[field]; !found {
			return false
		}
	}
	return true
}

func Decode(encoded string) (Config, error) {
	var value Config
	if len(encoded) > MaxBytes || json.Unmarshal([]byte(encoded), &value) != nil {
		return Config{}, ErrInvalid
	}
	return value, nil
}

func Encode(value Config) (string, error) {
	value, err := Normalize(value)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func Normalize(value Config) (Config, error) {
	normalized, _, err := prepare(value)
	return normalized, err
}

type span struct {
	start, end, percent int
}

func prepare(value Config) (Config, []span, error) {
	if len(value.TimeZone) == 0 || len(value.TimeZone) > 64 || !calendar.ValidZone(value.TimeZone) ||
		!validPercent(value.DefaultPercent) || len(value.Periods) > MaxPeriods {
		return Config{}, nil, ErrInvalid
	}
	value = value.Clone()
	if value.Periods == nil {
		value.Periods = []Period{}
	}
	spans := make([]span, 0, len(value.Periods)*7+1)
	for index := range value.Periods {
		period := &value.Periods[index]
		start, startOK := clockSeconds(period.StartTime, false)
		end, endOK := clockSeconds(period.EndTime, true)
		if !startOK || !endOK || start == end || !validPercent(period.Percent) ||
			len(period.Weekdays) < 1 || len(period.Weekdays) > 7 {
			return Config{}, nil, ErrInvalid
		}
		sort.Ints(period.Weekdays)
		for index, day := range period.Weekdays {
			if day < 1 || day > 7 || index > 0 && day == period.Weekdays[index-1] {
				return Config{}, nil, ErrInvalid
			}
			left, right := (day-1)*daySeconds+start, (day-1)*daySeconds+end
			if end < start {
				right += daySeconds
			}
			if right > weekSeconds {
				spans = append(spans, span{left, weekSeconds, period.Percent}, span{0, right - weekSeconds, period.Percent})
			} else {
				spans = append(spans, span{left, right, period.Percent})
			}
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	for index := 1; index < len(spans); index++ {
		if spans[index].start < spans[index-1].end {
			return Config{}, nil, ErrInvalid
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxBytes {
		return Config{}, nil, ErrInvalid
	}
	return value, spans, nil
}

func validPercent(percent int) bool { return percent >= 0 && percent <= 100 }

func clockSeconds(value string, allowEnd bool) (int, bool) {
	if allowEnd && value == "24:00" {
		return daySeconds, true
	}
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	for _, index := range []int{0, 1, 3, 4} {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	hour, minute := int(value[0]-'0')*10+int(value[1]-'0'), int(value[3]-'0')*10+int(value[4]-'0')
	return hour*3600 + minute*60, hour < 24 && minute < 60
}
