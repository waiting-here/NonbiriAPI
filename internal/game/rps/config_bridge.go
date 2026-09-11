package rps

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
)

func snapshotKeys() []string {
	return append([]string{game.GamesEnabledKey}, (rpsconfig.Codec{}).Keys()...)
}
