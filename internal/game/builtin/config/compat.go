package config

import (
	"encoding/json"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	biddingconfig "github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/compat"
	"github.com/waiting-here/NonbiriAPI/internal/game/fishing"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	likesconfig "github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
)

// ConfigSnapshot is the typed compatibility view used by central database
// validation. All compilation and public module projections remain in codecs.
type ConfigSnapshot struct {
	GamesEnabled   bool
	FishingEnabled bool
	Fishing        fishing.Config
	Rules          *fishing.Ruleset
	LinkLink       linklinkconfig.LinkLinkConfig
	RPS            rpsconfig.RPSConfig
	Bidding        biddingconfig.Snapshot
	Likes          likesconfig.Snapshot
}

func CompileConfig(raw map[string]string) (ConfigSnapshot, error) {
	fish, err := fishingconfig.CompileConfig(raw)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	link, err := linklinkconfig.CompileConfig(raw)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	rps, err := rpsconfig.CompileConfig(raw)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	bidding, err := biddingconfig.CompileConfig(raw)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	likes, err := likesconfig.CompileConfig(raw)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	return ConfigSnapshot{GamesEnabled: fish.GamesEnabled, FishingEnabled: fish.FishingEnabled, Fishing: fish.Fishing, Rules: fish.Rules, LinkLink: link.LinkLink, RPS: rps.RPS, Bidding: bidding, Likes: likes}, nil
}

func SiteConfigKeys() []string {
	keys := []string{game.GamesEnabledKey}
	keys = append(keys, (fishingconfig.Codec{}).Keys()...)
	keys = append(keys, (linklinkconfig.Codec{}).Keys()...)
	keys = append(keys, (rpsconfig.Codec{}).Keys()...)
	keys = append(keys, (biddingconfig.Codec{}).Keys()...)
	return append(keys, (likesconfig.Codec{}).Keys()...)
}

func (snapshot ConfigSnapshot) GamesConfig(revision string) compat.GamesConfig {
	return compat.GamesConfig{Revision: revision, MasterEnabled: snapshot.GamesEnabled,
		Bidding: snapshot.Bidding.Wire(), Likes: snapshot.Likes.Wire(),
		Fishing:  (fishingconfig.Snapshot{GamesEnabled: snapshot.GamesEnabled, FishingEnabled: snapshot.FishingEnabled, Fishing: snapshot.Fishing, Rules: snapshot.Rules}).Wire(),
		LinkLink: (linklinkconfig.Snapshot{GamesEnabled: snapshot.GamesEnabled, LinkLink: snapshot.LinkLink}).Wire(),
		RPS:      (rpsconfig.Snapshot{GamesEnabled: snapshot.GamesEnabled, RPS: snapshot.RPS}).Wire()}
}

func CompileGamesConfig(config compat.GamesConfig) (ConfigSnapshot, map[string]string, error) {
	if !game.ValidConfigRevision(config.Revision) {
		return ConfigSnapshot{}, nil, game.ErrInvalidConfig
	}
	registry, err := Registry()
	if err != nil {
		return ConfigSnapshot{}, nil, err
	}
	fragments := map[string]json.RawMessage{game.FishingID: game.ConfigJSON(config.Fishing), game.LinkLinkID: game.ConfigJSON(config.LinkLink), game.RPSID: game.ConfigJSON(config.RPS), "bidding": game.ConfigJSON(config.Bidding), "likes": game.ConfigJSON(config.Likes)}
	raw := map[string]string{game.GamesEnabledKey: game.BoolRaw(config.MasterEnabled)}
	for _, module := range registry.Descriptors() {
		value, err := module.Codec.CompileWire(fragments[module.ID], config.MasterEnabled)
		if err != nil {
			return ConfigSnapshot{}, nil, err
		}
		for key, item := range value.Raw() {
			raw[key] = item
		}
	}
	snapshot, err := CompileConfig(raw)
	return snapshot, raw, err
}

func DecodeGamesConfigPatch(body []byte) (compat.GamesConfigPatch, error) {
	registry, err := Registry()
	if err != nil {
		return compat.GamesConfigPatch{}, err
	}
	if _, err = registry.DecodePatch(body); err != nil {
		return compat.GamesConfigPatch{}, err
	}
	var patch compat.GamesConfigPatch
	err = json.Unmarshal(body, &patch)
	return patch, err
}
