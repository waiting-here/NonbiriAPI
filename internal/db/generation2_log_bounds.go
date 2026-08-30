package db

import (
	"unicode"
	"unicode/utf8"
)

// Request-log/activity bounds shared by the retained Generation 2 metadata
// helpers.  Request accounting itself is owned by the later dispatch rail;
// these constants only keep the common read/aggregation projections bounded.
const (
	MaxTokenDelta     = int64(1) << 40
	MaxLogPageLimit   = 100
	maxLogStatus      = 599
	maxLogErrorCodeLen = 64
)

func validOptionalStoredText(value string, maxRunes int) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}
