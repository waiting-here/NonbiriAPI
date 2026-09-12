// Package compat assembles the existing public game DTOs.
package compat

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
)

type GamesConfig struct {
	Revision      string                            `json:"revision"`
	MasterEnabled bool                              `json:"master_enabled"`
	Fishing       fishingconfig.FishingWireConfig   `json:"fishing"`
	LinkLink      linklinkconfig.LinkLinkWireConfig `json:"linklink"`
	RPS           rpsconfig.RPSWireConfig           `json:"rps"`
}

// GamesSnapshot is the exact user-facing configuration/readiness projection.
type GamesSnapshot struct {
	GameBalance     string                 `json:"game_balance"`
	ServerNow       int64                  `json:"server_now"`
	Balance         string                 `json:"balance"`
	TutorialRPSSeen bool                   `json:"tutorial_rps_seen"`
	GamesEnabled    bool                   `json:"games_enabled"`
	Fishing         FishingSnapshotModule  `json:"fishing"`
	LinkLink        LinkLinkSnapshotModule `json:"linklink"`
	RPS             RPSSnapshotModule      `json:"rps"`
}

type FishingSnapshotModule struct {
	Enabled    bool                            `json:"enabled"`
	Available  bool                            `json:"available"`
	BaitPrices fishingconfig.FishingBaitPrices `json:"bait_prices"`
}
type LinkLinkSnapshotModule struct {
	Enabled bool                                       `json:"enabled"`
	Specs   map[string]linklinkconfig.LinkLinkWireSpec `json:"specs"`
}
type RPSSnapshotModule struct {
	Enabled bool                             `json:"enabled"`
	Modes   map[string]rpsconfig.RPSWireMode `json:"modes"`
}
type GamesConfigPatch struct {
	ExpectedRevision string                              `json:"expected_revision"`
	MasterEnabled    *bool                               `json:"master_enabled,omitempty"`
	Fishing          *fishingconfig.FishingConfigPatch   `json:"fishing,omitempty"`
	LinkLink         *linklinkconfig.LinkLinkConfigPatch `json:"linklink,omitempty"`
	RPS              *rpsconfig.RPSConfigPatch           `json:"rps,omitempty"`
}

// Merge assembles validated partial DTO fragments. Each module codec still
// compiles the result before the host writes configuration.
func (patch GamesConfigPatch) Merge(current GamesConfig) (GamesConfig, error) {
	if patch.ExpectedRevision != current.Revision {
		return GamesConfig{}, game.ErrRevisionConflict
	}
	body := game.ConfigJSON(patch)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return GamesConfig{}, err
	}
	delete(fields, "expected_revision")
	var result GamesConfig
	if err := game.MergeConfigFields(game.ConfigJSON(current), game.ConfigJSON(fields), &result); err != nil {
		return GamesConfig{}, err
	}
	return result, nil
}
