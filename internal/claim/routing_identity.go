package claim

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

// physicalCharityKeyID identifies one resource, credential generation, and
// normalized outbound origin without storing or comparing credential bytes.
func physicalCharityKeyID(endpointKeyID, secretRefID int64, baseURL string) ([32]byte, error) {
	if endpointKeyID <= 0 || secretRefID <= 0 {
		return [32]byte{}, ErrInvariant
	}
	_, origin, err := egress.CanonicalEndpointTarget(baseURL)
	if err != nil {
		return [32]byte{}, ErrInvariant
	}
	input := make([]byte, 0, len(origin)+40)
	input = append(input, "charity-physical-key-v1\x00"...)
	input = binary.BigEndian.AppendUint64(input, uint64(endpointKeyID))
	input = binary.BigEndian.AppendUint64(input, uint64(secretRefID))
	input = binary.BigEndian.AppendUint64(input, uint64(len(origin)))
	input = append(input, origin...)
	return sha256.Sum256(input), nil
}
