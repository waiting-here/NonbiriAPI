package calendar

import "time"

// RecurrencePeriod resolves boundaries independently from the original wall
// clock, retaining the intended day after a short month or a DST adjustment.
// A future anchor ends the initial partial period that begins at effective.
func RecurrencePeriod(now, effective int64, anchor, interval, name string) (Period, error) {
	if now < effective || effective < 0 || now > MaxInstant || (interval != "day" && interval != "week" && interval != "month") {
		return Period{}, ErrInvalidTime
	}
	first, err := Resolve(anchor, name)
	if err != nil || first.Instant < 0 {
		return Period{}, ErrInvalidTime
	}
	if now < first.Instant {
		return Period{effective, first.Instant}, nil
	}
	wall, err := time.Parse(wallLayout, anchor)
	if err != nil {
		return Period{}, ErrInvalidTime
	}
	zone, err := lookupZone(name)
	if err != nil {
		return Period{}, err
	}
	local := time.Unix(now, 0).In(zone.location)
	index := max(recurrenceIndex(wall, local, interval), 0)
	for attempt := 0; attempt < 8; attempt++ {
		start, err := recurrenceBoundary(wall, interval, name, zone, index)
		if err != nil {
			return Period{}, err
		}
		if start.Instant > now {
			if index == 0 {
				return Period{}, ErrInvalidTime
			}
			index--
			continue
		}
		end, err := recurrenceBoundary(wall, interval, name, zone, index+1)
		if err != nil {
			return Period{}, err
		}
		if start.Instant < end.Instant && now < end.Instant {
			return Period{max(effective, start.Instant), end.Instant}, nil
		}
		index++
	}
	return Period{}, ErrInvalidTime
}

func recurrenceIndex(wall, local time.Time, interval string) int {
	if interval == "month" {
		return (local.Year()-wall.Year())*12 + int(local.Month()-wall.Month())
	}
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	baseDay := time.Date(wall.Year(), wall.Month(), wall.Day(), 0, 0, 0, 0, time.UTC)
	days := int((day.Unix() - baseDay.Unix()) / 86400)
	if interval == "week" {
		return days / 7
	}
	return days
}

func recurrenceBoundary(wall time.Time, interval, name string, zone registeredZone, index int) (ResolveResult, error) {
	shifted, err := shiftWall(wall, interval, index)
	if err != nil {
		return ResolveResult{}, err
	}
	return resolveWall(shifted, name, zone)
}

// NextRecurrences returns exactly three transitions strictly after after.
func NextRecurrences(anchor, interval, name string, after int64) ([]ResolveResult, error) {
	if after < 0 || after > MaxInstant {
		return nil, ErrInvalidTime
	}
	first, err := Resolve(anchor, name)
	if err != nil || first.Instant < 0 {
		return nil, ErrInvalidTime
	}
	if interval != "day" && interval != "week" && interval != "month" {
		return nil, ErrInvalidTime
	}
	wall, _ := time.Parse(wallLayout, anchor)
	zone, err := lookupZone(name)
	if err != nil {
		return nil, err
	}
	index := max(0, recurrenceIndex(wall, time.Unix(after, 0).In(zone.location), interval)-1)
	out := make([]ResolveResult, 0, 3)
	for attempt := 0; attempt < 8; attempt++ {
		result, err := recurrenceBoundary(wall, interval, name, zone, index+attempt)
		if err != nil {
			return nil, err
		}
		if result.Instant <= after {
			continue
		}
		out = append(out, result)
		after = result.Instant
		if len(out) == 3 {
			return out, nil
		}
	}
	return nil, ErrInvalidTime
}
