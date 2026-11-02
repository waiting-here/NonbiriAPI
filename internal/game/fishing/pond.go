package fishing

import "math"

// RollPondGift uses its own random stream after all ordinary catches and
// economic calculations. An empty pond consumes no randomness.
func RollPondGift(queueSize uint64, source IntSource) (bool, error) {
	if queueSize == 0 {
		return false, nil
	}
	if queueSize > math.MaxUint64-20 {
		return false, ErrInvalidBound
	}
	value, err := draw(source, queueSize+20)
	return err == nil && value < queueSize, err
}
