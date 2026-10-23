package resources

import (
	"net/url"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func safeMainstreamMatchInput(raw string) bool {
	if strings.ContainsAny(raw, "?#") {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.User == nil
}

func matchingMainstreamEntries(channels []MainstreamChannelOption, raw string) []MainstreamEntryMatch {
	matches := make([]MainstreamEntryMatch, 0)
	target, _, err := egress.CanonicalEndpointTarget(raw)
	if err != nil {
		return matches
	}
	for _, channel := range channels {
		for _, entry := range channel.Entries {
			if !entry.Enabled || entry.ID == "" {
				continue
			}
			entryTarget, _, err := egress.CanonicalEndpointTarget(entry.BaseURL)
			if err == nil && entryTarget == target {
				matches = append(matches, MainstreamEntryMatch{ChannelID: channel.ID, EntryID: entry.ID})
			}
		}
	}
	return matches
}
