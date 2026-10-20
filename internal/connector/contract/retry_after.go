package contract

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRetryAfterUnix int64 = 253402300799

// ParseRetryAfter normalizes only the retry hints needed for key availability.
// The boolean also preserves a valid zero or expired hint for outcome handling.
func ParseRetryAfter(headers http.Header, received time.Time) (int64, bool) {
	var latest int64
	valid := false
	for _, name := range []string{"Retry-After", "X-Retry-After"} {
		for _, raw := range headers.Values(name) {
			until, ok := parseRetryAfterValue(raw, name == "X-Retry-After", received)
			if ok && (!valid || until > latest) {
				latest, valid = until, true
			}
		}
	}
	return latest, valid
}

func parseRetryAfterValue(raw string, extended bool, received time.Time) (int64, bool) {
	value := strings.Trim(raw, " 	")
	if value == "" || received.Unix() < 0 || received.Unix() > maxRetryAfterUnix {
		return 0, false
	}
	digits := true
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			digits = false
			break
		}
	}
	if !digits {
		at, err := http.ParseTime(value)
		if err != nil || at.Unix() > maxRetryAfterUnix {
			return 0, false
		}
		return max(0, at.Unix()), true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	if extended {
		switch {
		case len(value) <= 9:
		case len(value) == 10 && n >= 1_000_000_000:
			return n, true
		case len(value) == 13 && n >= 1_000_000_000_000:
			return (n + 999) / 1000, true
		default:
			return 0, false
		}
	}
	base := received.Unix()
	if n == 0 {
		return base, true
	}
	if received.Nanosecond() != 0 {
		base++
	}
	if base > maxRetryAfterUnix || n > maxRetryAfterUnix-base {
		return 0, false
	}
	return base + n, true
}

// ResponseObservation is a normalized, non-billing fact captured at headers.
type ResponseObservation struct {
	HasRetryAfter    bool
	Status           int
	Stream           bool
	ObservedAt       int64
	RateLimitedUntil int64
}
