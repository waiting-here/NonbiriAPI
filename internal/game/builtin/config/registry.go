// Package config supplies the storage-neutral built-in module catalog.
// Database validation can use it without importing persistent runtimes.
package config

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
)

func Registry() (*game.Registry, error) {
	registry := game.NewRegistry()
	for _, descriptor := range []game.ModuleDescriptor{fishingconfig.Descriptor(), linklinkconfig.Descriptor(), rpsconfig.Descriptor()} {
		if err := registry.Register(descriptor); err != nil {
			return nil, err
		}
	}
	if err := registry.Seal(); err != nil {
		return nil, err
	}
	return registry, nil
}
