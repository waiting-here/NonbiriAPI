package config

import "github.com/waiting-here/NonbiriAPI/internal/game"

func Modules() []game.ModuleDescriptor {
	registry, err := Registry()
	if err != nil {
		panic(err)
	}
	return registry.Descriptors()
}

func Resolve(id string, version int) (game.ModuleDescriptor, error) {
	registry, err := Registry()
	if err != nil {
		return game.ModuleDescriptor{}, err
	}
	return registry.Resolve(id, version)
}

func ResolveMode(id, mode string) error {
	for _, module := range Modules() {
		if module.ID == id {
			return module.ResolveMode(mode)
		}
	}
	return game.ErrUnknownGame
}

func ResolveSpec(id, spec string) error {
	for _, module := range Modules() {
		if module.ID == id {
			return module.ResolveSpec(spec)
		}
	}
	return game.ErrUnknownGame
}

func validateContract(contract interface {
	Validate(game.ModuleDescriptor) error
}) error {
	for _, module := range Modules() {
		if err := contract.Validate(module); err == nil {
			return nil
		}
	}
	return game.ErrInvalidContract
}
