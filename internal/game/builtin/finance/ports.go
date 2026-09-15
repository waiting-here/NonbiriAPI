// Package finance contains the audited adapters to the closed ledger commands.
package finance

import (
	"github.com/waiting-here/NonbiriAPI/internal/game"
	ports "github.com/waiting-here/NonbiriAPI/internal/game/finance"
	fishingconfig "github.com/waiting-here/NonbiriAPI/internal/game/fishing/config"
	linklinkconfig "github.com/waiting-here/NonbiriAPI/internal/game/linklink/config"
	rpsconfig "github.com/waiting-here/NonbiriAPI/internal/game/rps/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

type Capabilities struct {
	Fishing   ports.Fishing
	LinkLink  ports.LinkLink
	RPS       ports.RPS
	Duel      ports.Duel
	Blackjack ports.Blackjack
}

// ForModule grants only the operations registered for this compiled module.
// There is no command-name escape hatch or generic balance mutation method.
func ForModule(id string) (Capabilities, error) {
	switch id {
	case game.FishingID:
		return Capabilities{Fishing: fishingPort{onboarding{fishingconfig.Descriptor()}}}, nil
	case game.LinkLinkID:
		return Capabilities{LinkLink: linkLinkPort{onboarding{linklinkconfig.Descriptor()}}}, nil
	case game.RPSID:
		return Capabilities{RPS: rpsPort{onboarding{rpsconfig.Descriptor()}}}, nil
	case "bidding", "likes":
		return Capabilities{Duel: duelPort{game: id}}, nil
	case game.BlackjackID:
		return Capabilities{Blackjack: blackjackPort{}}, nil
	default:
		return Capabilities{}, ledger.ErrInvalidPlan
	}
}
