package calendar

import "time"

// LocalAt projects an actual instant through the pinned zone registry. Unlike
// Resolve, it never shifts a missing wall clock or chooses between repeated ones.
func LocalAt(instant int64, name string) (time.Time, error) {
	if instant < 0 || instant > MaxInstant {
		return time.Time{}, ErrInvalidTime
	}
	zone, err := lookupZone(name)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(instant, 0).In(zone.location), nil
}
