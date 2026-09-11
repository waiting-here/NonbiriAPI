package linklink

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
)

func snapshotKeys() []string {
	return append([]string{game.GamesEnabledKey}, (linklinkconfig.Codec{}).Keys()...)
}
