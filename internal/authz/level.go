package authz

import (
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// AutomaticLevel projects the monotonic automatic level from validated
// account and threshold values. A zero threshold disables that promotion.
func AutomaticLevel(stored int, donation db.U128, thresholds [5]int64) int {
	amount := donation.Big()
	for level := 2; level <= 4; level++ {
		if level > stored && thresholds[level] > 0 && amount.Cmp(big.NewInt(thresholds[level])) >= 0 {
			stored = level
		}
	}
	return stored
}
