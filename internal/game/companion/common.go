package companion

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	gw "github.com/waiting-here/NonbiriAPI/internal/game/gwent/engine"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/catalog"
	lk "github.com/waiting-here/NonbiriAPI/internal/game/likes/engine"
	"io"
	"slices"
)

func catalogFor(game, mode string) (any, string, error) {
	var value any
	switch game {
	case "bidding":
		if mode != "standard" {
			return nil, "", errors.New("bidding mode must be standard")
		}
		value = Obj{"version": 1, "rounds": 13, "joker_multiplier": 2, "ties": "carry_then_discard", "reward_ranks": []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}}
	case "gwent":
		if mode != "standard" {
			return nil, "", errors.New("gwent mode must be standard")
		}
		value = Obj{"version": gw.Version, "cards": gw.Catalog(), "presets": gw.Presets()}
	case "likes":
		if mode != "quick" && mode != "standard" {
			return nil, "", errors.New("likes mode must be quick or standard")
		}
		snap, e := catalog.Public(mode)
		if e != nil {
			return nil, "", e
		}
		return snap, snap.ContentHash, nil
	default:
		return nil, "", errors.New("unknown game")
	}
	return value, digest(encode(value)), nil
}

type likesLimits struct {
	Role    string   `json:"role,omitempty"`
	Harness Raw      `json:"harness,omitempty"`
	Skills  []string `json:"skills,omitempty"`
}

func (l likesLimits) allows(sel lk.Selection) bool {
	if l.Role != "" && sel.Role != l.Role {
		return false
	}
	if l.Harness != nil && !member(encode(sel.Harness), []Raw{l.Harness}) {
		return false
	}
	for _, key := range l.Skills {
		if !slices.Contains(sel.Skills, key) {
			return false
		}
	}
	return true
}
func likesExamples(e *lk.Engine, c catalog.Config, l likesLimits) []Raw {
	var out []Raw
	harnesses := []*string{nil}
	for _, h := range c.Harnesses {
		harnesses = append(harnesses, &h.ID)
	}
	for _, role := range c.Roles {
		found := false
		for _, h := range harnesses {
			sel := lk.Selection{Role: role.ID, Harness: h, Skills: slices.Clone(l.Skills)}
			if !l.allows(sel) {
				continue
			}
			if e.ValidateSelection(sel) == nil {
				out = append(out, encode(sel))
				break
			}
			for _, sk := range c.Skills {
				if slices.Contains(sel.Skills, sk.ID) || !sk.Stable {
					continue
				}
				trial := sel
				trial.Skills = append(slices.Clone(sel.Skills), sk.ID)
				if e.ValidateSelection(trial) == nil {
					out = append(out, encode(trial))
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	return out
}

type gwLimits struct {
	Faction string `json:"faction,omitempty"`
}

func encode(v any) Raw {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// Compare JSON objects independent of key order. Preserve integers exactly.
func normal(b Raw) (string, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return "", e
	}
	if d.Decode(new(any)) != io.EOF {
		return "", errors.New("trailing JSON")
	}
	return string(encode(v)), nil
}
func member(action Raw, choices []Raw) bool {
	key, e := normal(action)
	if e != nil {
		return false
	}
	for _, v := range choices {
		other, _ := normal(v)
		if other == key {
			return true
		}
	}
	return false
}
