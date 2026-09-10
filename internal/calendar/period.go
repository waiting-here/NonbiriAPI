package calendar

import "time"

const MaxLookbackSeconds = int64(35 * 24 * 60 * 60)

type Period struct{ Start, End int64 }

// Add advances one interval while preserving the local clock. A month clamps
// the day to the target month's last day; hourly intervals use actual seconds.
func Add(instant int64, interval, zone string) (int64, error) {
	return shift(instant, interval, zone, 1)
}

func Subtract(instant int64, interval, zone string) (int64, error) {
	left, err := shift(instant, interval, zone, -1)
	if err != nil {
		return 0, err
	}
	// A month spans at most 31 nominal days and each offset has magnitude
	// below one day. Gap-forward and fold-later resolution cannot increase
	// this upper bound. Keep a guard for future registry changes as well.
	if left > instant || instant-left > MaxLookbackSeconds {
		return 0, ErrInvalidTime
	}
	return left, nil
}

func shift(instant int64, interval, name string, direction int) (int64, error) {
	if instant < MinInternalInstant || instant > MaxInstant {
		return 0, ErrInvalidTime
	}
	zone, err := lookupZone(name)
	if err != nil {
		return 0, err
	}
	if interval == "1h" || interval == "5h" {
		seconds := int64(3600)
		if interval == "5h" {
			seconds = 18000
		}
		result := instant + int64(direction)*seconds
		if result < MinInternalInstant || result > MaxInstant {
			return 0, ErrInvalidTime
		}
		return result, nil
	}
	local := time.Unix(instant, 0).In(zone.location)
	wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
	wall, err = shiftWall(wall, interval, direction)
	if err != nil {
		return 0, err
	}
	result, err := resolveWall(wall, name, zone)
	return result.Instant, err
}

func shiftWall(wall time.Time, interval string, direction int) (time.Time, error) {
	switch interval {
	case "day":
		return wall.AddDate(0, 0, direction), nil
	case "week":
		return wall.AddDate(0, 0, 7*direction), nil
	case "month":
		target := time.Date(wall.Year(), wall.Month()+time.Month(direction), 1, wall.Hour(), wall.Minute(), wall.Second(), 0, time.UTC)
		last := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
		return target.AddDate(0, 0, min(wall.Day(), last)-1), nil
	default:
		return time.Time{}, ErrInvalidTime
	}
}

// NaturalPeriod returns the nonempty [Start,End) interval containing now.
// Weekly boundaries use ISO weekdays 1 (Monday) through 7 (Sunday); callers
// pass zero for day/month. Each boundary is resolved independently, keeping
// the intended calendar anchor even if a midnight or whole day was skipped.
func NaturalPeriod(now int64, interval, name string, weekStartsOn int) (Period, error) {
	if now < 0 || now > MaxInstant ||
		(interval == "week" && (weekStartsOn < 1 || weekStartsOn > 7)) ||
		(interval != "week" && weekStartsOn != 0) {
		return Period{}, ErrInvalidTime
	}
	zone, err := lookupZone(name)
	if err != nil {
		return Period{}, err
	}
	local := time.Unix(now, 0).In(zone.location)
	anchor := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	switch interval {
	case "day":
	case "week":
		weekday := (int(anchor.Weekday())+6)%7 + 1
		anchor = anchor.AddDate(0, 0, -(weekday-weekStartsOn+7)%7)
	case "month":
		anchor = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return Period{}, ErrInvalidTime
	}
	// At most one whole local day is skipped or repeated by this registry.
	// The bounded search also handles now in the earlier half of a repeated
	// midnight: it belongs to the preceding period until the later boundary.
	for attempt := 0; attempt < 8; attempt++ {
		start, err := resolveWall(anchor, name, zone)
		if err != nil {
			return Period{}, err
		}
		if start.Instant > now {
			anchor, err = shiftWall(anchor, interval, -1)
			if err != nil {
				return Period{}, err
			}
			continue
		}
		next, err := shiftWall(anchor, interval, 1)
		if err != nil {
			return Period{}, err
		}
		end, err := resolveWall(next, name, zone)
		if err != nil {
			return Period{}, err
		}
		if start.Instant < end.Instant && now < end.Instant {
			return Period{start.Instant, end.Instant}, nil
		}
		anchor = next
	}
	return Period{}, ErrInvalidTime
}
