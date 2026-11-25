package runner

import "errors"

// Upload permissions are fixed by the release. A configured worker cannot enable them.
const UploadsEnabled = false

const (
	biddingUploadsEnabled = false
	likesUploadsEnabled   = false
	gwentUploadsEnabled   = false
)

func GameUploadsEnabled(game string) bool {
	switch game {
	case "bidding":
		return biddingUploadsEnabled
	case "likes":
		return likesUploadsEnabled
	case "gwent":
		return gwentUploadsEnabled
	default:
		return false
	}
}

func UploadsEnabledFor(game string) bool {
	return UploadsEnabled && GameUploadsEnabled(game)
}

var ErrUploadsDisabled = errors.New("only the built-in companion is available in this release")
