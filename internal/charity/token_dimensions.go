package charity

import (
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func actualTokenVector(input claim.CharityAttemptInput, reserved donationquota.TokenVector) (donationquota.TokenVector, error) {
	if !reserved.Valid() {
		return donationquota.TokenVector{}, claim.ErrInvariant
	}
	total, err := actualTokenCount(input, reserved.Total)
	if err != nil {
		return donationquota.TokenVector{}, err
	}
	if !input.ResponseStarted {
		zero := int64(0)
		return donationquota.TokenVector{Input: &zero, Output: &zero}, nil
	}
	if input.UsageUnknown || !input.Usage.Present {
		return reserved, nil
	}
	output := input.Usage.OutputTokens
	// actualTokenCount already validates all normalized components and overflow.
	prompt := total - output
	return donationquota.TokenVector{Total: total, Input: &prompt, Output: &output}, nil
}

func equalTokenValue(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
