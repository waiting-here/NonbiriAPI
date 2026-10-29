package managementlog

import (
	"encoding/json"
	"math/big"
	"strings"
	"unicode/utf8"
)

// Details distinguishes transaction snapshots from historical records which
// did not retain them. Impacts count row writes, not distinct target objects.
type Details struct {
	Version      int             `json:"version,omitempty"`
	PreviousName string          `json:"previous_name,omitempty"`
	State        string          `json:"state,omitempty"`
	Related      []RelatedObject `json:"related,omitempty"`
	Impacts      []Impact        `json:"impacts,omitempty"`
	Truncated    bool            `json:"truncated,omitempty"`
}
type RelatedObject struct {
	Kind         string   `json:"kind"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	PreviousName string   `json:"previous_name,omitempty"`
	State        string   `json:"state"`
	Changes      []Change `json:"changes,omitempty"`
}
type Impact struct {
	Kind    string `json:"kind"`
	Created int64  `json:"created,string"`
	Updated int64  `json:"updated,string"`
	Deleted int64  `json:"deleted,string"`
	Sampled int64  `json:"sampled,string"`
	Omitted int64  `json:"omitted,string"`
}

func boundedText(value string, limit int) (string, bool) {
	value = strings.ToValidUTF8(strings.ReplaceAll(strings.ReplaceAll(value, "\x00", ""), "\r", ""), "")
	if utf8.RuneCountInString(value) <= limit {
		return value, false
	}
	return string([]rune(value)[:limit-1]) + "…", true
}

func normalizeChanges(changes []Change) ([]Change, bool) {
	dropped := len(changes) > 64
	if dropped {
		changes = changes[:64]
	}
	out := append([]Change(nil), changes...)
	for i := range out {
		for _, field := range []**string{&out[i].Before, &out[i].After} {
			if *field == nil {
				continue
			}
			value := **field
			if parts := strings.Split(value, ":"); len(parts) == 3 && parts[0] == "@i128" {
				if number, ok := new(big.Int).SetString(parts[2], 16); ok {
					if parts[1] == "-1" {
						number.Neg(number)
					}
					value = number.String()
				}
			}
			if strings.HasPrefix(value, "@u128:") {
				if number, ok := new(big.Int).SetString(strings.TrimPrefix(value, "@u128:"), 16); ok {
					value = number.String()
				}
			}
			value, truncated := boundedText(value, 512)
			out[i].Truncated = out[i].Truncated || truncated
			*field = &value
		}
	}
	// The retained schema bounds the complete encoded array as well as each value.
	for limit := 256; ; limit /= 2 {
		encoded, _ := json.Marshal(out)
		if len(encoded) <= 16384 {
			return out, dropped
		}
		for i := range out {
			for _, field := range []**string{&out[i].Before, &out[i].After} {
				if *field != nil {
					value, truncated := boundedText(**field, limit)
					out[i].Truncated = out[i].Truncated || truncated
					*field = &value
				}
			}
		}
		if limit <= 2 {
			return out, dropped
		}
	}
}

func normalizeDetails(details Details) Details {
	for {
		encoded, _ := json.Marshal(details)
		if len(encoded) <= 65536 {
			return details
		}
		details.Truncated = true
		if len(details.Related) == 0 {
			return details
		}
		last := len(details.Related) - 1
		if len(details.Related[last].Changes) > 0 {
			details.Related[last].Changes = nil
		} else {
			details.Related = details.Related[:last]
		}
	}
}
