// Package rewardasset defines assets shared by reward delivery adapters.
package rewardasset

type Asset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	NameEN    string `json:"name_en"`
	Group     string `json:"group"`
	Precision int    `json:"precision"`
	Maximum   string `json:"maximum"`
}
type Item struct {
	Asset    string `json:"asset"`
	Quantity string `json:"quantity"`
}
type Result struct {
	Item
	Granted   string `json:"granted"`
	Skipped   string `json:"skipped,omitempty"`
	Converted *Item  `json:"converted,omitempty"`
}
