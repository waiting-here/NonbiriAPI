package runtime

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
)

func snapshotKeys() []string {
	return append([]string{game.GamesEnabledKey}, (fishingconfig.Codec{}).Keys()...)
}

func (service *Service) available() bool {
	return service != nil && !service.closed.Load() && (service.capability == nil || service.capability())
}
