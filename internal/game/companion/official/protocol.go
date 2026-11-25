package official

import (
	"context"
	"encoding/json"
)

const Protocol = "nonbiri.decision/1"

type Request struct {
	Protocol string                   `json:"protocol"`
	Game     string                   `json:"game"`
	Mode     string                   `json:"mode"`
	ID       string                   `json:"decision_id"`
	Seat     int                      `json:"seat"`
	Round    int                      `json:"round"`
	Phase    string                   `json:"phase"`
	View     json.RawMessage          `json:"view"`
	Memory   string                   `json:"memory"`
	History  map[string]historySource `json:"history"`
	Space    struct {
		Examples []json.RawMessage `json:"examples"`
	} `json:"space"`
}

func Decide(ctx context.Context, game string, data json.RawMessage) (json.RawMessage, error) {
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	if req.Protocol != Protocol || req.Game != game || req.ID == "" || req.Seat < 0 || req.Seat > 1 {
		return nil, errDecision
	}
	var action any
	var err error
	hint := openingFromHistory(ctx, req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch game {
	case "bidding":
		action, err = biddingDecision(req.Phase, req.Seat, req.Round, req.View, hint.BidShift)
	case "likes":
		action, err = likesDecision(req.Mode, req.Phase, req.Seat, req.View, req.Space.Examples, hint.Role)
	case "gwent":
		action, err = gwentDecision(req.Phase, req.View, req.Space.Examples, hint.Faction)
	default:
		return nil, errDecision
	}
	if err != nil {
		return nil, err
	}
	memory, _ := json.Marshal(hint)
	return json.Marshal(struct {
		Protocol string `json:"protocol"`
		ID       string `json:"decision_id"`
		Action   any    `json:"action"`
		Memory   string `json:"memory"`
	}{Protocol, req.ID, action, string(memory)})
}
