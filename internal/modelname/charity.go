// Package modelname owns public model-name markers shared across domains.
package modelname

import "strings"

// CharityPrefix is a wire marker, independent of the display language.
const CharityPrefix = "[公益]"

// IsCharity checks the reserved prefix. It does not validate the remaining
// provider/model identity or grant access to the referenced model.
func IsCharity(name string) bool { return strings.HasPrefix(name, CharityPrefix) }
