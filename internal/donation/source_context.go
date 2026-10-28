package donation

import (
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/endpointtags"
)

// The effective view applies current manual labels and domain-rule precedence.
// Its indexed base URL predicate stays tied to the selected donation key.
var endpointTagsProjectionSQL = func() string {
	values := endpointtags.Values()
	for i, tag := range values {
		values[i] = "SELECT '" + tag + "' AS tag"
	}
	// Aggregate only the finite vocabulary. EXISTS preserves predicate pushdown
	// through the effective view instead of aggregating unrelated addresses.
	return `(SELECT COALESCE(group_concat(candidate.tag,','),'') FROM (` + strings.Join(values, " UNION ALL ") + `) candidate
WHERE EXISTS(SELECT 1 FROM effective_endpoint_tags t WHERE t.base_url=dk.canonical_base_url AND t.tag=candidate.tag))`
}()

func sourceTags(value string) []string {
	if value == "" {
		return nil
	}
	tags := strings.Split(value, ",")
	slices.Sort(tags)
	return slices.Compact(tags)
}

func ownerSource(value AdminSafeSource) SafeSource {
	return SafeSource{Kind: value.Kind, ConnectorType: value.ConnectorType, BaseURL: value.BaseURL, ChannelID: value.ChannelID, Name: value.Name}
}
