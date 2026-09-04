package rps

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var (
	bigOne      = big.NewInt(1)
	bigThousand = big.NewInt(1000)
	bigTenK     = big.NewInt(10000)
	maxU128     = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), bigOne)
)

func cloneBig(value *big.Int) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(value)
}

func u128(value *big.Int) (db.U128, error) {
	encoded, err := db.U128FromBig(value)
	if err != nil {
		return db.U128{}, ErrInvariant
	}
	return encoded, nil
}

func u256(value *big.Int) (db.U256, error) {
	encoded, err := db.U256FromBig(value)
	if err != nil {
		return db.U256{}, ErrInvariant
	}
	return encoded, nil
}

func addU128(values ...db.U128) (db.U128, error) {
	total := new(big.Int)
	for _, value := range values {
		total.Add(total, value.Big())
	}
	return u128(total)
}

func incU128(value db.U128) (db.U128, error) {
	return u128(new(big.Int).Add(value.Big(), bigOne))
}

func subU128(left, right db.U128) (db.U128, error) {
	value := new(big.Int).Sub(left.Big(), right.Big())
	if value.Sign() < 0 {
		return db.U128{}, ErrInvariant
	}
	return u128(value)
}

func addU256Value(value db.U256, increment *big.Int) (db.U256, error) {
	if increment == nil || increment.Sign() < 0 {
		return db.U256{}, ErrInvariant
	}
	return u256(new(big.Int).Add(value.Big(), increment))
}

func formatMilli(value *big.Int) string {
	if value == nil {
		return "0"
	}
	sign := ""
	magnitude := cloneBig(value)
	if magnitude.Sign() < 0 {
		sign = "-"
		magnitude.Abs(magnitude)
	}
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(magnitude, bigThousand, remainder)
	if remainder.Sign() == 0 {
		return sign + whole.String()
	}
	fraction := strconv.FormatInt(remainder.Int64()+1000, 10)[1:]
	fraction = strings.TrimRight(fraction, "0")
	return sign + whole.String() + "." + fraction
}

func parsePositiveCreditInteger(value string) (*big.Int, error) {
	if value == "" || value[0] < '1' || value[0] > '9' || len(value) > 39 {
		return nil, ErrInvalidRequest
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return nil, ErrInvalidRequest
		}
	}
	credits, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return nil, ErrInvalidRequest
	}
	milli := new(big.Int).Mul(credits, bigThousand)
	if milli.Sign() <= 0 || milli.Cmp(maxU128) > 0 {
		return nil, ErrInvalidRequest
	}
	return milli, nil
}

type cutBreakdown struct {
	Platform *big.Int
	Welfare  *big.Int
	Thursday *big.Int
	Net      *big.Int
}

func cutsForInput(input *big.Int, pumps PumpsBP) (cutBreakdown, error) {
	if input == nil || input.Sign() <= 0 || pumps.Platform < 0 || pumps.Welfare < 0 || pumps.Thursday < 0 ||
		pumps.Platform+pumps.Welfare+pumps.Thursday >= 10000 {
		return cutBreakdown{}, ErrInvariant
	}
	component := func(bp int) *big.Int {
		return new(big.Int).Quo(new(big.Int).Mul(input, big.NewInt(int64(bp))), bigTenK)
	}
	result := cutBreakdown{
		Platform: component(pumps.Platform), Welfare: component(pumps.Welfare), Thursday: component(pumps.Thursday),
	}
	result.Net = new(big.Int).Sub(cloneBig(input), result.Platform)
	result.Net.Sub(result.Net, result.Welfare)
	result.Net.Sub(result.Net, result.Thursday)
	if result.Net.Sign() <= 0 {
		return cutBreakdown{}, ErrInvariant
	}
	return result, nil
}

func addCut(total *cutBreakdown, next cutBreakdown) {
	if total.Platform == nil {
		total.Platform, total.Welfare, total.Thursday, total.Net = new(big.Int), new(big.Int), new(big.Int), new(big.Int)
	}
	total.Platform.Add(total.Platform, next.Platform)
	total.Welfare.Add(total.Welfare, next.Welfare)
	total.Thursday.Add(total.Thursday, next.Thursday)
	total.Net.Add(total.Net, next.Net)
}

func validGesture(value string) bool {
	return value == GestureRock || value == GestureScissors || value == GesturePaper
}

func gestureBeats(left, right string) bool {
	return left == GestureRock && right == GestureScissors ||
		left == GestureScissors && right == GesturePaper ||
		left == GesturePaper && right == GestureRock
}

type revealEvaluation struct {
	Weights [3]int
	Code    string
	Tie     bool
}

func evaluateReveal(gestures [3]string, surrendered [3]bool) (revealEvaluation, error) {
	for seat := range gestures {
		if !validGesture(gestures[seat]) {
			return revealEvaluation{}, ErrInvariant
		}
	}
	surrenderCount := 0
	for _, surrendered := range surrendered {
		if surrendered {
			surrenderCount++
		}
	}
	if surrenderCount == 2 {
		result := revealEvaluation{Code: RevealTwoSurrenders}
		for seat := range surrendered {
			if !surrendered[seat] {
				result.Weights[seat] = 6
			}
		}
		return result, nil
	}
	if surrenderCount == 1 {
		var active [2]int
		cursor := 0
		for seat := range surrendered {
			if !surrendered[seat] {
				active[cursor] = seat
				cursor++
			}
		}
		if gestures[active[0]] == gestures[active[1]] {
			result := revealEvaluation{Code: RevealOneSurrenderTie}
			result.Weights[active[0]], result.Weights[active[1]] = 3, 3
			return result, nil
		}
		result := revealEvaluation{Code: RevealOneSurrenderDecided}
		if gestureBeats(gestures[active[0]], gestures[active[1]]) {
			result.Weights[active[0]], result.Weights[active[1]] = 4, 2
		} else {
			result.Weights[active[0]], result.Weights[active[1]] = 2, 4
		}
		return result, nil
	}
	if surrenderCount != 0 {
		return revealEvaluation{}, ErrInvariant
	}
	if gestures[0] == gestures[1] && gestures[1] == gestures[2] {
		return revealEvaluation{Weights: [3]int{2, 2, 2}, Code: RevealThreeEqual, Tie: true}, nil
	}
	if gestures[0] != gestures[1] && gestures[1] != gestures[2] && gestures[0] != gestures[2] {
		return revealEvaluation{Weights: [3]int{2, 2, 2}, Code: RevealAllDistinct, Tie: true}, nil
	}
	counts := map[string]int{}
	for _, gesture := range gestures {
		counts[gesture]++
	}
	var uniqueSeat int
	for seat, gesture := range gestures {
		if counts[gesture] == 1 {
			uniqueSeat = seat
			break
		}
	}
	other := (uniqueSeat + 1) % 3
	if other == uniqueSeat || counts[gestures[other]] != 2 {
		other = (uniqueSeat + 2) % 3
	}
	if gestureBeats(gestures[uniqueSeat], gestures[other]) {
		result := revealEvaluation{Weights: [3]int{1, 1, 1}, Code: RevealOneBeatsTwo}
		result.Weights[uniqueSeat] = 4
		return result, nil
	}
	result := revealEvaluation{Weights: [3]int{3, 3, 3}, Code: RevealTwoBeatOne}
	result.Weights[uniqueSeat] = 0
	return result, nil
}

func payouts(pool *big.Int, weights [3]int) ([3]*big.Int, *big.Int, error) {
	result := [3]*big.Int{new(big.Int), new(big.Int), new(big.Int)}
	if pool == nil || pool.Sign() < 0 {
		return result, nil, ErrInvariant
	}
	returned := new(big.Int)
	totalWeight := 0
	for seat, weight := range weights {
		if weight < 0 || weight > 6 {
			return result, nil, ErrInvariant
		}
		totalWeight += weight
		result[seat].Quo(new(big.Int).Mul(pool, big.NewInt(int64(weight))), big.NewInt(6))
		returned.Add(returned, result[seat])
	}
	if totalWeight != 6 {
		return result, nil, ErrInvariant
	}
	carry := new(big.Int).Sub(pool, returned)
	if carry.Sign() < 0 {
		return result, nil, ErrInvariant
	}
	return result, carry, nil
}

func permanentMultiplier(mode string, completedBase *big.Int) (*big.Int, error) {
	if completedBase == nil || completedBase.Sign() < 0 {
		return nil, ErrInvariant
	}
	if mode != "deathmatch" || completedBase.Cmp(big.NewInt(9)) < 0 {
		return big.NewInt(1), nil
	}
	value := new(big.Int).Sub(completedBase, big.NewInt(9))
	value.Quo(value, big.NewInt(3))
	value.Add(value, big.NewInt(2))
	if value.Cmp(maxU128) > 0 {
		return nil, ErrInvariant
	}
	return value, nil
}

func paidPlanMultiplier(mode string, permanent, completedPaidStreak *big.Int) (*big.Int, error) {
	if permanent == nil || permanent.Sign() <= 0 || completedPaidStreak == nil || completedPaidStreak.Sign() < 0 {
		return nil, ErrInvariant
	}
	if mode != "deathmatch" || completedPaidStreak.Cmp(big.NewInt(3)) < 0 {
		return cloneBig(permanent), nil
	}
	increment := new(big.Int).Sub(completedPaidStreak, big.NewInt(2))
	result := new(big.Int).Add(permanent, increment)
	if result.Cmp(maxU128) > 0 {
		return nil, ErrInvariant
	}
	return result, nil
}

// sessionFutureRows implements the frozen §6.11 proof:
// H=16*(90*ceil(D/60)+ceil(D/5)+91)+1.
func sessionFutureRows(startedAt int64) (db.U128, error) {
	const maximumUnixSecond = int64(253402300799)
	if startedAt < 0 || startedAt > maximumUnixSecond {
		return db.U128{}, ErrInvariant
	}
	duration := new(big.Int).SetInt64(maximumUnixSecond - startedAt)
	ceil := func(value *big.Int, divisor int64) *big.Int {
		adjusted := new(big.Int).Add(value, big.NewInt(divisor-1))
		return adjusted.Quo(adjusted, big.NewInt(divisor))
	}
	actions := new(big.Int).Mul(big.NewInt(90), ceil(duration, 60))
	actions.Add(actions, ceil(duration, 5))
	actions.Add(actions, big.NewInt(91))
	actions.Mul(actions, big.NewInt(16))
	actions.Add(actions, bigOne)
	if actions.BitLen() > 43 || actions.Sign() <= 0 {
		return db.U128{}, ErrInvariant
	}
	return u128(actions)
}

func walletNet(returned, starting *big.Int) (int, db.U128, error) {
	if returned == nil || starting == nil || returned.Sign() < 0 || starting.Sign() < 0 {
		return 0, db.U128{}, ErrInvariant
	}
	delta := new(big.Int).Sub(returned, starting)
	sign := delta.Sign()
	delta.Abs(delta)
	mag, err := u128(delta)
	return sign, mag, err
}

func signedAdd(leftSign int, leftMag *big.Int, rightSign int, rightMag *big.Int) (int, db.U128, error) {
	if leftSign < -1 || leftSign > 1 || rightSign < -1 || rightSign > 1 || leftMag == nil || rightMag == nil ||
		leftMag.Sign() < 0 || rightMag.Sign() < 0 || leftSign == 0 && leftMag.Sign() != 0 ||
		leftSign != 0 && leftMag.Sign() == 0 || rightSign == 0 && rightMag.Sign() != 0 || rightSign != 0 && rightMag.Sign() == 0 {
		return 0, db.U128{}, ErrInvariant
	}
	left := cloneBig(leftMag)
	if leftSign < 0 {
		left.Neg(left)
	}
	right := cloneBig(rightMag)
	if rightSign < 0 {
		right.Neg(right)
	}
	left.Add(left, right)
	sign := left.Sign()
	left.Abs(left)
	value, err := u128(left)
	return sign, value, err
}

func profitRate(sessionCount, profitableCount *big.Int) (int, error) {
	if sessionCount == nil || profitableCount == nil || sessionCount.Sign() <= 0 || profitableCount.Sign() < 0 || profitableCount.Cmp(sessionCount) > 0 {
		return 0, ErrInvariant
	}
	rate := new(big.Int).Quo(new(big.Int).Mul(profitableCount, bigTenK), sessionCount)
	if !rate.IsInt64() || rate.Int64() < 0 || rate.Int64() > 10000 {
		return 0, ErrInvariant
	}
	return int(rate.Int64()), nil
}

func formatPercentBP(bp int) string {
	whole, fraction := bp/100, bp%100
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	return strings.TrimRight(strconv.Itoa(whole)+"."+strconv.FormatInt(int64(fraction+100), 10)[1:], "0")
}
