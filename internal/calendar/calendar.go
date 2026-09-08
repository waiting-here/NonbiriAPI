// Package calendar resolves local wall clocks and calendar boundaries using
// a fixed, embedded IANA registry, independently of the host's time zone.
package calendar

import (
	"errors"
	"time"
)

const (
	AdjustmentNone       = "none"
	AdjustmentGapShifted = "gap_shifted"
	AdjustmentFoldLater  = "fold_later"
	MinInternalInstant   = int64(-62167219200)
	MaxInstant           = int64(253402300799)
	wallLayout           = "2006-01-02T15:04:05"
)

var ErrInvalidTime = errors.New("calendar: invalid time or zone")

type ResolveResult struct {
	Instant       int64
	Local         string
	TimeZone      string
	OffsetSeconds int
	Adjustment    string
}

// Resolve accepts exactly YYYY-MM-DDTHH:mm:ss. Repeated wall clocks select the
// later instant; missing wall clocks advance by the actual transition size.
// Internal callers may resolve pre-Unix boundaries; public APIs enforce zero
// as their lower bound.
func Resolve(local, zoneName string) (ResolveResult, error) {
	if !validLocal(local) {
		return ResolveResult{}, ErrInvalidTime
	}
	wall, err := time.Parse(wallLayout, local)
	if err != nil {
		return ResolveResult{}, ErrInvalidTime
	}
	zone, err := lookupZone(zoneName)
	if err != nil {
		return ResolveResult{}, err
	}
	return resolveWall(wall, zoneName, zone)
}

func resolveWall(wall time.Time, name string, zone registeredZone) (ResolveResult, error) {
	if wall.Year() < 0 || wall.Year() > 9999 {
		return ResolveResult{}, ErrInvalidTime
	}
	var latest time.Time
	matches := 0
	for _, offset := range zone.offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second).In(zone.location)
		if sameWall(candidate, wall) {
			matches++
			if matches == 1 || candidate.After(latest) {
				latest = candidate
			}
		}
	}
	if matches > 0 {
		adjustment := AdjustmentNone
		if matches > 1 {
			adjustment = AdjustmentFoldLater
		}
		return resolvedResult(latest, name, adjustment)
	}
	// Inspect both sides of each candidate's interval. The POSIX continuation
	// can report a start earlier than the final explicit TZif transition, so
	// the preceding interval's end is also needed. Accept only a real offset
	// change containing this wall clock, never an unrelated historical offset.
	for _, offset := range zone.offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second).In(zone.location)
		start, end := candidate.ZoneBounds()
		for _, boundary := range []time.Time{start, end} {
			if boundary.IsZero() {
				continue
			}
			_, before := boundary.Add(-time.Second).Zone()
			_, after := boundary.Zone()
			left, right := boundary.Unix()+int64(before), boundary.Unix()+int64(after)
			shifted := wall.Add(-time.Duration(before) * time.Second).In(zone.location)
			if after > before && wall.Unix() >= left && wall.Unix() < right &&
				sameWall(shifted, wall.Add(time.Duration(after-before)*time.Second)) {
				return resolvedResult(shifted, name, AdjustmentGapShifted)
			}
		}
	}
	return ResolveResult{}, ErrInvalidTime
}

func resolvedResult(instant time.Time, name, adjustment string) (ResolveResult, error) {
	unix := instant.Unix()
	if unix < MinInternalInstant || unix > MaxInstant {
		return ResolveResult{}, ErrInvalidTime
	}
	_, offset := instant.Zone()
	return ResolveResult{unix, instant.Format(wallLayout), name, offset, adjustment}, nil
}

func sameWall(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	ah, amin, as := a.Clock()
	bh, bmin, bs := b.Clock()
	return ay == by && am == bm && ad == bd && ah == bh && amin == bmin && as == bs
}

func validLocal(value string) bool {
	if len(value) != 19 {
		return false
	}
	for i := range len(value) {
		switch i {
		case 4, 7:
			if value[i] != '-' {
				return false
			}
		case 10:
			if value[i] != 'T' {
				return false
			}
		case 13, 16:
			if value[i] != ':' {
				return false
			}
		default:
			if value[i] < '0' || value[i] > '9' {
				return false
			}
		}
	}
	return true
}
