// Package finance contains the audited adapters to the closed ledger commands.
package finance

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type Capabilities struct {
	Fishing  ports.Fishing
	LinkLink ports.LinkLink
	RPS      ports.RPS
}

// ForModule grants only the operations registered for this compiled module.
// There is no command-name escape hatch or generic balance mutation method.
func ForModule(id string) (Capabilities, error) {
	switch id {
	case game.FishingID:
		return Capabilities{Fishing: fishingPort{}}, nil
	case game.LinkLinkID:
		return Capabilities{LinkLink: linkLinkPort{}}, nil
	case game.RPSID:
		return Capabilities{RPS: rpsPort{}}, nil
	default:
		return Capabilities{}, ledger.ErrInvalidPlan
	}
}
