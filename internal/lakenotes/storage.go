package lakenotes

import "github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"

// Storage formats advance only when persisted encoding or interpretation
// changes. Catalog/rule revisions do not automatically invalidate a profile.
const (
	profileStorageVersion = 1
	castStorageVersion    = 1
)

func decodeStoredProfile(version int, raw []byte) (rules.Profile, error) {
	if version != profileStorageVersion {
		return rules.Profile{}, ErrInvariant
	}
	return rules.DecodeProfile(raw)
}

func decodeStoredCast(version int, raw []byte) (rules.Cast, error) {
	if version != castStorageVersion {
		return rules.Cast{}, ErrInvariant
	}
	// A live checkpoint still requires its exact simulation rules.
	return rules.DecodeCast(raw)
}
