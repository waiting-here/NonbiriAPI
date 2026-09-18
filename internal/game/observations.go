package game

import "encoding/json"

type ContinueItem struct {
	Game       string `json:"game"`
	ResourceID string `json:"resource_id"`
	State      string `json:"state"`
	RouteID    string `json:"route_id"`
}

type PendingResult struct {
	Game       string `json:"game"`
	ResourceID string `json:"resource_id"`
	CreatedAt  int64  `json:"created_at"`
	RouteID    string `json:"route_id"`
}

type HomeSummary struct {
	Continue       []ContinueItem  `json:"continue"`
	PendingResults []PendingResult `json:"pending_results"`
}

type ActiveCounts struct {
	Games  []GameCount  `json:"games"`
	Queues []QueueCount `json:"queues"`
}

type GameCount struct {
	Game  string  `json:"game"`
	Mode  *string `json:"mode"`
	Spec  *string `json:"spec"`
	Phase *string `json:"phase"`
	Count string  `json:"count"`
}

type QueueCount struct {
	Game  string `json:"game"`
	Mode  string `json:"mode"`
	Count string `json:"count"`
}

type UserSnapshot struct {
	Config json.RawMessage
	Fields map[string]json.RawMessage
}
