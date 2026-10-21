// Package modeltype defines the public operations enabled for a logical model.
package modeltype

import (
	"database/sql/driver"
	"errors"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

type Set []contract.Operation

func Default() Set       { return Set{contract.OperationChatCompletions, contract.OperationResponses} }
func (s Set) Clone() Set { return append(Set{}, s...) }
func (s Set) Mask() (int64, error) {
	if len(s) == 0 || len(s) > len(contract.AllOperations()) {
		return 0, errors.New("invalid model types")
	}
	var mask int64
	seen := map[contract.Operation]bool{}
	for _, operation := range s {
		var bit int64
		for i, current := range contract.AllOperations() {
			if current == operation {
				bit = 1 << i
				break
			}
		}
		if bit == 0 {
			return 0, errors.New("invalid model type")
		}
		if seen[operation] {
			return 0, errors.New("duplicate model type")
		}
		mask |= bit
		seen[operation] = true
	}
	if mask&17 != 0 {
		mask |= 17
	}
	return mask, nil
}
func (s Set) Valid() bool { _, err := s.Mask(); return err == nil }
func (s Set) Supports(operation contract.Operation) bool {
	for _, current := range s {
		if current == operation || chatCategory(current) && chatCategory(operation) {
			return true
		}
	}
	return false
}
func (s Set) Value() (driver.Value, error) { return s.Mask() }
func (s *Set) Scan(value any) error {
	mask, ok := value.(int64)
	if !ok || mask < 1 || mask >= 1<<len(contract.AllOperations()) {
		return errors.New("invalid stored model types")
	}
	*s = Set{}
	if mask&17 != 0 {
		mask |= 17
	}
	for i, operation := range contract.AllOperations() {
		if mask&(1<<i) != 0 {
			*s = append(*s, operation)
		}
	}
	return nil
}

func chatCategory(operation contract.Operation) bool {
	return operation == contract.OperationChatCompletions || operation == contract.OperationResponses
}
