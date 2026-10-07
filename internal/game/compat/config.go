// Package compat assembles the existing public game DTOs.
package compat

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	biddingconfig "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	blackjackconfig "github.com/waiting-here/NonbiriAPI/internal/game/blackjack/config"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	gwentconfig "github.com/waiting-here/NonbiriAPI/internal/game/gwent/config"
	likesconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
	catchconfig "github.com/waiting-here/NonbiriAPI/internal/game/steadycatch/config"
	lakeconfig "github.com/waiting-here/NonbiriAPI/internal/lakenotes/config"
)

type GamesConfig struct {
	LakeNotes     lakeconfig.Wire                   `json:"lakenotes"`
	SteadyCatch   catchconfig.Wire                  `json:"steadycatch"`
	Gwent         gwentconfig.Wire                  `json:"gwent"`
	Revision      string                            `json:"revision"`
	MasterEnabled bool                              `json:"master_enabled"`
	Fishing       fishingconfig.FishingWireConfig   `json:"fishing"`
	LinkLink      linklinkconfig.LinkLinkWireConfig `json:"linklink"`
	RPS           rpsconfig.RPSWireConfig           `json:"rps"`
	Bidding       biddingconfig.Wire                `json:"bidding"`
	Likes         likesconfig.Wire                  `json:"likes"`
	Blackjack     blackjackconfig.Wire              `json:"blackjack"`
}

// GamesSnapshot is the exact user-facing configuration/readiness projection.
type GamesSnapshot struct {
	LakeNotes       lakeconfig.Wire                    `json:"lakenotes"`
	SteadyCatch     CatchSnapshotModule                `json:"steadycatch"`
	Gwent           GwentSnapshotModule                `json:"gwent"`
	Onboarding      map[string]game.OnboardingProgress `json:"onboarding"`
	GameBalance     string                             `json:"game_balance"`
	ServerNow       int64                              `json:"server_now"`
	Balance         string                             `json:"balance"`
	TutorialRPSSeen bool                               `json:"tutorial_rps_seen"`
	GamesEnabled    bool                               `json:"games_enabled"`
	Fishing         FishingSnapshotModule              `json:"fishing"`
	LinkLink        LinkLinkSnapshotModule             `json:"linklink"`
	RPS             RPSSnapshotModule                  `json:"rps"`
	Bidding         BiddingSnapshotModule              `json:"bidding"`
	Likes           LikesSnapshotModule                `json:"likes"`
	Blackjack       BlackjackSnapshotModule            `json:"blackjack"`
}

type CatchSnapshotModule struct {
	catchconfig.Wire
	Available    bool `json:"available"`
	FirstCleared bool `json:"first_cleared"`
}

type BlackjackSnapshotModule struct {
	blackjackconfig.Wire
	Available       bool   `json:"available"`
	ConfigHash      string `json:"config_hash"`
	QueueCapacity   int    `json:"queue_capacity"`
	Seats           int    `json:"seats"`
	SeatingSeconds  int    `json:"seating_seconds"`
	DecisionSeconds int    `json:"decision_seconds"`
	RoundSeconds    int    `json:"round_seconds"`
}

type DuelSnapshotMode struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Ticket    string `json:"ticket"`
	RakeBP    struct {
		Platform int `json:"platform"`
		Welfare  int `json:"welfare"`
		Thursday int `json:"thursday"`
	} `json:"rake_bp"`
	TermsHash   string `json:"terms_hash"`
	ContentHash string `json:"content_hash"`
}
type BiddingSnapshotModule struct {
	Enabled       bool                        `json:"enabled"`
	Available     bool                        `json:"available"`
	Modes         map[string]DuelSnapshotMode `json:"modes"`
	QueueSeconds  int                         `json:"queue_seconds"`
	JokerSeconds  int                         `json:"joker_seconds"`
	BidSeconds    int                         `json:"bid_seconds"`
	QueueCapacity int                         `json:"queue_capacity"`
}
type LikesSnapshotModule struct {
	Enabled           bool                        `json:"enabled"`
	Available         bool                        `json:"available"`
	Modes             map[string]DuelSnapshotMode `json:"modes"`
	QueueSeconds      int                         `json:"queue_seconds"`
	PlanSeconds       int                         `json:"plan_seconds"`
	SettlementSeconds int                         `json:"settlement_seconds"`
	QueueCapacity     int                         `json:"queue_capacity"`
}

type FishingSnapshotModule struct {
	BlueFishChanceBPS int                             `json:"blue_fish_chance_bps"`
	Enabled           bool                            `json:"enabled"`
	Available         bool                            `json:"available"`
	BaitPrices        fishingconfig.FishingBaitPrices `json:"bait_prices"`
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
	LakeNotes        *lakeconfig.Patch                   `json:"lakenotes,omitempty"`
	SteadyCatch      *catchconfig.Patch                  `json:"steadycatch,omitempty"`
	Gwent            *gwentconfig.Patch                  `json:"gwent,omitempty"`
	ExpectedRevision string                              `json:"expected_revision"`
	MasterEnabled    *bool                               `json:"master_enabled,omitempty"`
	Fishing          *fishingconfig.FishingConfigPatch   `json:"fishing,omitempty"`
	LinkLink         *linklinkconfig.LinkLinkConfigPatch `json:"linklink,omitempty"`
	RPS              *rpsconfig.RPSConfigPatch           `json:"rps,omitempty"`
	Bidding          *biddingconfig.Patch                `json:"bidding,omitempty"`
	Likes            *likesconfig.Patch                  `json:"likes,omitempty"`
	Blackjack        *blackjackconfig.Patch              `json:"blackjack,omitempty"`
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

type GwentSnapshotModule struct {
	Enabled         bool                        `json:"enabled"`
	Available       bool                        `json:"available"`
	Modes           map[string]DuelSnapshotMode `json:"modes"`
	QueueSeconds    int                         `json:"queue_seconds"`
	MulliganSeconds int                         `json:"mulligan_seconds"`
	TurnSeconds     int                         `json:"turn_seconds"`
	ChoiceSeconds   int                         `json:"choice_seconds"`
	QueueCapacity   int                         `json:"queue_capacity"`
}
