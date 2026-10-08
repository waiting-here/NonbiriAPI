// Package modeltype defines the public operations enabled for a logical model.
package modeltype

import (
	"database/sql/driver"
	"errors"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type Set []contract.Operation

func Default() Set       { return Set{contract.OperationChatCompletions} }
func (s Set) Clone() Set { return append(Set{}, s...) }
func (s Set) Mask() (int64, error) {
	if len(s) == 0 || len(s) > 3 {
		return 0, errors.New("invalid model types")
	}
	var mask int64
	for _, operation := range s {
		var bit int64
		switch operation {
		case contract.OperationChatCompletions:
			bit = 1
		case contract.OperationEmbeddings:
			bit = 2
		case contract.OperationImagesGenerations:
			bit = 4
		default:
			return 0, errors.New("invalid model type")
		}
		if mask&bit != 0 {
			return 0, errors.New("duplicate model type")
		}
		mask |= bit
	}
	return mask, nil
}
func (s Set) Valid() bool { _, err := s.Mask(); return err == nil }
func (s Set) Supports(operation contract.Operation) bool {
	for _, current := range s {
		if current == operation {
			return true
		}
	}
	return false
}
func (s Set) Value() (driver.Value, error) { return s.Mask() }
func (s *Set) Scan(value any) error {
	mask, ok := value.(int64)
	if !ok || mask < 1 || mask > 7 {
		return errors.New("invalid stored model types")
	}
	*s = Set{}
	for i, operation := range []contract.Operation{contract.OperationChatCompletions, contract.OperationEmbeddings, contract.OperationImagesGenerations} {
		if mask&(1<<i) != 0 {
			*s = append(*s, operation)
		}
	}
	return nil
}
