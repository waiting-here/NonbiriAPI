package charitydiscount

import (
	"errors"
	"sort"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
)

const maxZoneTransitions = 32

var ErrTransitionBudget = errors.New("charity discount: time-zone transition budget exhausted")

type Fixed struct {
	Enabled bool
	Percent int
	StartAt *int64
	EndAt   *int64
}

type Effective struct {
	Source       string `json:"source"`
	Percent      int    `json:"percent"`
	NextChangeAt *int64 `json:"next_change_at"`
}

// PercentAt is the admission and billing path. It performs no future search.
func PercentAt(fixed Fixed, recurring Config, at int64) (int, error) {
	recurring, spans, err := prepareInputs(fixed, recurring, at)
	if err != nil {
		return 0, err
	}
	if fixed.Enabled {
		return fixedPercent(fixed, at), nil
	}
	if !recurring.Enabled {
		return 100, nil
	}
	local, err := calendar.LocalAt(at, recurring.TimeZone)
	if err != nil {
		return 0, err
	}
	return periodPercent(spans, recurring.DefaultPercent, weekSecond(local)), nil
}

// Evaluate also projects the next actual percentage change. A fixed switch
// keeps precedence before its start and after its end.
func Evaluate(fixed Fixed, recurring Config, at int64) (Effective, error) {
	recurring, spans, err := prepareInputs(fixed, recurring, at)
	if err != nil {
		return Effective{}, err
	}
	out := Effective{Source: "none", Percent: 100}
	if fixed.Enabled {
		out.Source, out.Percent = "fixed", fixedPercent(fixed, at)
		if fixed.Percent == 100 || fixed.StartAt != nil && fixed.EndAt != nil && *fixed.StartAt == *fixed.EndAt {
			return out, nil
		}
		if fixed.StartAt != nil && at < *fixed.StartAt {
			out.NextChangeAt = new(*fixed.StartAt)
		} else if fixed.EndAt != nil && at < *fixed.EndAt {
			out.NextChangeAt = new(*fixed.EndAt)
		}
		return out, nil
	}
	if !recurring.Enabled {
		return out, nil
	}
	out.Source = "recurring"
	local, err := calendar.LocalAt(at, recurring.TimeZone)
	if err != nil {
		return Effective{}, err
	}
	out.Percent = periodPercent(spans, recurring.DefaultPercent, weekSecond(local))
	boundaries := effectiveBoundaries(spans, recurring.DefaultPercent)
	if len(boundaries) == 0 {
		return out, nil
	}
	out.NextChangeAt, err = nextChange(recurring.TimeZone, spans, recurring.DefaultPercent, boundaries, at)
	if err != nil {
		return Effective{}, err
	}
	return out, nil
}

func prepareInputs(fixed Fixed, recurring Config, at int64) (Config, []span, error) {
	if at < 0 || at > calendar.MaxInstant || !validPercent(fixed.Percent) ||
		fixed.StartAt != nil && (*fixed.StartAt < 0 || *fixed.StartAt > calendar.MaxInstant) ||
		fixed.EndAt != nil && (*fixed.EndAt < 0 || *fixed.EndAt > calendar.MaxInstant) ||
		fixed.StartAt != nil && fixed.EndAt != nil && *fixed.StartAt > *fixed.EndAt {
		return Config{}, nil, ErrInvalid
	}
	return prepare(recurring)
}

func fixedPercent(fixed Fixed, at int64) int {
	if fixed.StartAt != nil && at < *fixed.StartAt || fixed.EndAt != nil && at >= *fixed.EndAt {
		return 100
	}
	return fixed.Percent
}

func weekSecond(local time.Time) int {
	day := (int(local.Weekday()) + 6) % 7
	return day*daySeconds + local.Hour()*3600 + local.Minute()*60 + local.Second()
}

func periodPercent(spans []span, fallback, second int) int {
	index := sort.Search(len(spans), func(index int) bool { return spans[index].start > second }) - 1
	if index >= 0 && second < spans[index].end {
		return spans[index].percent
	}
	return fallback
}

func effectiveBoundaries(spans []span, fallback int) []int {
	candidates := make([]int, 0, len(spans)*2)
	for _, period := range spans {
		candidates = append(candidates, period.start, period.end%weekSeconds)
	}
	sort.Ints(candidates)
	boundaries := make([]int, 0, len(candidates))
	for index, candidate := range candidates {
		if index > 0 && candidates[index-1] == candidate {
			continue
		}
		before := (candidate + weekSeconds - 1) % weekSeconds
		if periodPercent(spans, fallback, before) != periodPercent(spans, fallback, candidate) {
			boundaries = append(boundaries, candidate)
		}
	}
	return boundaries
}

// In a fixed-offset interval the next weekly boundary is an exact duration
// away. Offset changes compete with that candidate, including gaps, folds and
// skipped dates; reevaluating each side avoids ambiguous wall-clock resolution.
func nextChange(zone string, spans []span, fallback int, boundaries []int, after int64) (*int64, error) {
	cursor := after
	transitions := 0
	for {
		local, err := calendar.LocalAt(cursor, zone)
		if err != nil {
			return nil, err
		}
		second := weekSecond(local)
		index := sort.Search(len(boundaries), func(index int) bool { return boundaries[index] > second })
		var delta int
		if index == len(boundaries) {
			delta = weekSeconds - second + boundaries[0]
		} else {
			delta = boundaries[index] - second
		}
		candidate := cursor + int64(delta)
		_, end := local.ZoneBounds()
		zoneChange := !end.IsZero() && end.Unix() > cursor && end.Unix() <= candidate
		if zoneChange {
			candidate = end.Unix()
		}
		if candidate > calendar.MaxInstant {
			return nil, nil
		}
		before, err := calendar.LocalAt(candidate-1, zone)
		if err != nil {
			return nil, err
		}
		current, err := calendar.LocalAt(candidate, zone)
		if err != nil {
			return nil, err
		}
		if periodPercent(spans, fallback, weekSecond(before)) != periodPercent(spans, fallback, weekSecond(current)) {
			return &candidate, nil
		}
		if !zoneChange {
			return nil, ErrInvalid
		}
		transitions++
		if transitions >= maxZoneTransitions {
			return nil, ErrTransitionBudget
		}
		cursor = candidate
	}
}
