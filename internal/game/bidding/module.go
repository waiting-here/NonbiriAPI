package bidding

import (
	"github.com/waiting-here/NonbiriAPI/internal/game/bidding/config"
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
)

func New(options duel.Options) (*duel.Service, error) {
	options.Descriptor = config.Descriptor()
	options.Rules = Rules{}
	if options.AI == nil {
		options.AI = AIAdapter{}
	}
	return duel.New(options)
}
