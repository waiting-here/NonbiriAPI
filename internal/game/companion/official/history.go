package official

import (
	"context"
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game/companion/historyio"
)

// Keep one small opening tendency across the decisions in this match. The
// host supplies each source only when the resulting program is allowed to read it.
type opening struct {
	BidShift float64 `json:"bid_shift,omitempty"`
	Role     string  `json:"role,omitempty"`
	Faction  string  `json:"faction,omitempty"`
}

type historySource struct {
	Snapshot string `json:"snapshot"`
	Count    int    `json:"count"`
}

func openingFromHistory(ctx context.Context, req Request) opening {
	var hint opening
	if req.Memory != "" {
		_ = json.Unmarshal([]byte(req.Memory), &hint)
		return hint
	}
	src := req.History["personal"]
	if src.Count == 0 {
		src = req.History["global"]
	}
	if src.Count == 0 || src.Snapshot == "" {
		return hint
	}
	args, _ := json.Marshal(map[string]any{"snapshot": src.Snapshot, "limit": 1})
	page, err := historyio.Call(ctx, "list", args)
	var list struct {
		Items []struct {
			Record string `json:"record"`
			Bytes  int    `json:"bytes"`
		} `json:"items"`
	}
	if err != nil || json.Unmarshal(page, &list) != nil || len(list.Items) == 0 || list.Items[0].Bytes > 512<<10 {
		return hint
	}
	args, _ = json.Marshal(map[string]string{"snapshot": src.Snapshot, "record": list.Items[0].Record})
	body, err := historyio.Call(ctx, "read", args)
	var record struct {
		Initial struct {
			Human int `json:"human_seat"`
		} `json:"initial"`
		Actions []struct {
			Phase   string             `json:"phase"`
			Actions [2]json.RawMessage `json:"actions"`
		} `json:"actions"`
		Result struct {
			Winner *int `json:"winner"`
		} `json:"result"`
	}
	if err != nil || json.Unmarshal(body, &record) != nil || record.Initial.Human < 0 || record.Initial.Human > 1 {
		return hint
	}
	count, sum := 0, 0
	for _, w := range record.Actions {
		if w.Phase == "bid" && count < 4 {
			var human struct {
				Card int `json:"card"`
			}
			if json.Unmarshal(w.Actions[record.Initial.Human], &human) == nil && human.Card >= 1 && human.Card <= 13 {
				count++
				sum += human.Card
			}
		}
		if w.Phase == "setup" && record.Result.Winner != nil && *record.Result.Winner == 1-record.Initial.Human {
			var built struct {
				Role    string `json:"role"`
				Faction string `json:"faction"`
			}
			if json.Unmarshal(w.Actions[1-record.Initial.Human], &built) == nil {
				hint.Role, hint.Faction = built.Role, built.Faction
			}
		}
	}
	if count > 0 {
		hint.BidShift = (float64(sum)/float64(count) - 7) / 2
	}
	return hint
}
