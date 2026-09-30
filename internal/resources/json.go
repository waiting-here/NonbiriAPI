package resources

import (
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

var errInvalidResourceJSON = errors.New("resources: invalid JSON object")

// Resource batches bound fields per object, rather than over the entire body.
func validateResourceJSONObject(data []byte) error {
	if strictjson.ValidateObjectPerObject(data) != nil {
		return errInvalidResourceJSON
	}
	return nil
}
