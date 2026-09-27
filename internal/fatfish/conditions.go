package fatfish

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

// Condition is a closed, monotone prerequisite expression. A zero Kind is
// the empty true expression; explicit all/any arrays must be nonempty.
type Condition struct {
	Kind     string
	Children []Condition
	NodeID   string
	Min      int
}

// ConditionHint is the user-safe prerequisite projection. A hidden leaf never
// carries a node ID or exact threshold until that node becomes visible.
type ConditionHint struct {
	Kind        string          `json:"kind"`
	Met         bool            `json:"met"`
	NodeID      string          `json:"node_id,omitempty"`
	Min         int             `json:"min,omitempty"`
	HiddenCount int             `json:"hidden_count,omitempty"`
	Children    []ConditionHint `json:"children,omitempty"`
}

func (c Condition) Project(best map[string]int, visible map[string]bool) ConditionHint {
	if (c.Kind == "passed" || c.Kind == "stars") && !visible[c.NodeID] {
		return ConditionHint{Kind: "hidden", HiddenCount: 1}
	}
	hint := ConditionHint{Kind: c.Kind, Met: c.Eligible(best), NodeID: c.NodeID, Min: c.Min}
	if c.Kind == "" {
		hint.Kind = "none"
	}
	for _, child := range c.Children {
		projected := child.Project(best, visible)
		hint.HiddenCount += projected.HiddenCount
		hint.Children = append(hint.Children, projected)
	}
	return hint
}

func ParseCondition(raw []byte) (Condition, error) {
	if len(raw) == 0 || len(raw) > 32768 || strictjson.ValidateObjectWithFieldLimit(raw, 128) != nil {
		return Condition{}, ErrInvalid
	}
	count := 0
	c, err := parseCondition(raw, 1, &count)
	if err != nil || count > 128 {
		return Condition{}, ErrInvalid
	}
	return c, nil
}

func parseCondition(raw []byte, depth int, count *int) (Condition, error) {
	if depth > 8 {
		return Condition{}, ErrInvalid
	}
	(*count)++
	if *count > 128 {
		return Condition{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Condition{}, ErrInvalid
	}
	if len(fields) == 0 {
		return Condition{}, nil
	}
	if len(fields) != 1 {
		return Condition{}, ErrInvalid
	}
	for kind, value := range fields {
		c := Condition{Kind: kind}
		switch kind {
		case "all", "any":
			var children []json.RawMessage
			if err := json.Unmarshal(value, &children); err != nil || len(children) == 0 || len(children) > 128 {
				return Condition{}, ErrInvalid
			}
			c.Children = make([]Condition, 0, len(children))
			for _, child := range children {
				parsed, err := parseCondition(child, depth+1, count)
				if err != nil {
					return Condition{}, err
				}
				c.Children = append(c.Children, parsed)
			}
		case "passed":
			if json.Unmarshal(value, &c.NodeID) != nil || !db.ValidateOpaqueID(c.NodeID, "ffn_") {
				return Condition{}, ErrInvalid
			}
		case "stars":
			var item map[string]json.RawMessage
			if json.Unmarshal(value, &item) != nil || len(item) != 2 {
				return Condition{}, ErrInvalid
			}
			if json.Unmarshal(item["node"], &c.NodeID) != nil || !db.ValidateOpaqueID(c.NodeID, "ffn_") {
				return Condition{}, ErrInvalid
			}
			min, err := canonicalInt(item["min"])
			if err != nil || min < 1 || min > 3 {
				return Condition{}, ErrInvalid
			}
			c.Min = min
		case "passed_count", "total_stars":
			min, err := canonicalInt(value)
			if err != nil || min < 0 {
				return Condition{}, ErrInvalid
			}
			c.Min = min
		default:
			return Condition{}, ErrInvalid
		}
		return c, nil
	}
	return Condition{}, ErrInvalid
}

func canonicalInt(raw []byte) (int, error) {
	if len(raw) == 0 || bytes.ContainsAny(raw, ".eE+") || string(raw) == "-0" {
		return 0, ErrInvalid
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || strconv.Itoa(n) != string(raw) {
		return 0, ErrInvalid
	}
	return n, nil
}

func (c Condition) ValidateReferences(stars map[string]int) error {
	switch c.Kind {
	case "":
		return nil
	case "all", "any":
		for _, child := range c.Children {
			if err := child.ValidateReferences(stars); err != nil {
				return err
			}
		}
	case "passed", "stars":
		max, ok := stars[c.NodeID]
		if !ok || c.Kind == "stars" && c.Min > max {
			return fmt.Errorf("%w: unavailable prerequisite node", ErrInvalid)
		}
	case "passed_count":
		if c.Min > len(stars) {
			return ErrInvalid
		}
	case "total_stars":
		total := 0
		for _, n := range stars {
			total += n
		}
		if c.Min > total {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (c Condition) Eligible(bestStars map[string]int) bool {
	switch c.Kind {
	case "":
		return true
	case "all":
		for _, child := range c.Children {
			if !child.Eligible(bestStars) {
				return false
			}
		}
		return true
	case "any":
		for _, child := range c.Children {
			if child.Eligible(bestStars) {
				return true
			}
		}
		return false
	case "passed":
		return bestStars[c.NodeID] > 0
	case "stars":
		return bestStars[c.NodeID] >= c.Min
	case "passed_count":
		count := 0
		for _, n := range bestStars {
			if n > 0 {
				count++
			}
		}
		return count >= c.Min
	case "total_stars":
		total := 0
		for _, n := range bestStars {
			total += n
		}
		return total >= c.Min
	default:
		return false
	}
}

// ReachableNodes uses the least fixed point, so a cycle is allowed exactly
// when some condition can admit an entry node from already reachable nodes.
func ReachableNodes(conditions map[string]Condition, maximumStars map[string]int) ([]string, error) {
	if len(conditions) == 0 || len(conditions) > 128 || len(conditions) != len(maximumStars) {
		return nil, ErrInvalid
	}
	for id, c := range conditions {
		if maximumStars[id] < 1 || maximumStars[id] > 3 || c.ValidateReferences(maximumStars) != nil {
			return nil, ErrInvalid
		}
	}
	reached := make(map[string]int, len(conditions))
	for {
		changed := false
		for id, c := range conditions {
			if reached[id] == 0 && c.Eligible(reached) {
				reached[id] = maximumStars[id]
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	if len(reached) != len(conditions) {
		return nil, ErrConflict
	}
	ids := make([]string, 0, len(reached))
	for id := range reached {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids, nil
}

func sortStrings(items []string) { sort.Strings(items) }
