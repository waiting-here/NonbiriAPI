package likes

import (
	"github.com/waiting-here/NonbiriAPI/internal/game/duel"
	"github.com/waiting-here/NonbiriAPI/internal/game/likes/config"
)

func New(options duel.Options) (*duel.Service, error) {
	rules, err := NewRules()
	if err != nil {
		return nil, err
	}
	options.Descriptor = config.Descriptor()
	options.Rules = rules
	return duel.New(options)
}
