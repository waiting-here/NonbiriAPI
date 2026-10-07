package lakenotes

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/lakenotes/rules"
)

// Profiles retain their encoding as the compatible catalog grows. Checkpoints
// carry a separate format version because movement state participates in replay.
const (
	profileStorageVersion = 1
	castStorageVersion    = 2
	priorCastRulesID      = "lake-notes-d1be1134679327c43a38766d857cb0d3131659d2049cf7ea667034bcf727255e"
)

func decodeStoredProfile(version int, raw []byte) (rules.Profile, error) {
	if version != profileStorageVersion {
		return rules.Profile{}, ErrInvariant
	}
	return rules.DecodeProfile(raw)
}

func decodeStoredCast(version int, identity string, raw []byte) (rules.Cast, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return rules.Cast{}, ErrInvariant
	}
	var savedID string
	if json.Unmarshal(fields["rules_id"], &savedID) != nil || savedID != identity {
		return rules.Cast{}, ErrInvariant
	}
	switch version {
	case 1:
		if identity != priorCastRulesID {
			return rules.Cast{}, ErrInvariant
		}
		// Keep the sampled encounter, motion RNG, progress and earned rewards.
		// New movement fields start at zero; already hooked fish need no new pause.
		fields["rules_id"], _ = json.Marshal(rules.RulesID)
		var err error
		raw, err = json.Marshal(fields)
		if err != nil {
			return rules.Cast{}, err
		}
	case castStorageVersion:
		if identity != rules.RulesID {
			return rules.Cast{}, ErrInvariant
		}
	default:
		return rules.Cast{}, ErrInvariant
	}
	return rules.DecodeCast(raw)
}
