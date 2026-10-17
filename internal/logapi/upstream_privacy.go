package logapi

import "github.com/waiting-here/NonbiriAPI/internal/textprivacy"

func redactUpstreamText(value *string) *string {
	if value == nil {
		return nil
	}
	redacted := textprivacy.IPv4(*value)
	return &redacted
}
