package db

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func progressionConfigDefaults() map[string]string {
	return map[string]string{
		"activity_loan_enabled":       "0",
		"activity_loan_tiers":         `["10000","100000","1000000"]`,
		"activity_loan_a_milli":       "900",
		"activity_loan_b_milli":       "1300",
		"game_blackjack_quick_stakes": `["1000","5000","10000","50000"]`,
	}
}

// LoanTerms uses integer coefficients: one thousand denotes one. All five
// monetary components are checked before narrowing any arbitrary-width value.
type LoanTerms struct {
	Principal, A, B                              int64
	Nominal, Disbursed, Fee, Repayment, Interest int64
}

func CalculateLoanTerms(principal, a, b string) (LoanTerms, error) {
	x, err := parseGenerationTwoUint(principal)
	if err != nil || x == 0 || x > uint64(MaxMoneyMilli/1000) {
		return LoanTerms{}, errors.New("invalid loan principal")
	}
	av, err := parseGenerationTwoUint(a)
	if err != nil || av < 1 || av > 999 {
		return LoanTerms{}, errors.New("invalid loan disbursement coefficient")
	}
	bv, err := parseGenerationTwoUint(b)
	if err != nil || bv < 1001 || bv > uint64(MaxMoneyMilli) {
		return LoanTerms{}, errors.New("invalid loan repayment coefficient")
	}
	t := LoanTerms{Principal: int64(x), A: int64(av), B: int64(bv)}
	for _, item := range []struct {
		factor uint64
		target *int64
	}{
		{1000, &t.Nominal}, {av, &t.Disbursed}, {1000 - av, &t.Fee}, {bv, &t.Repayment}, {bv - 1000, &t.Interest},
	} {
		value := new(big.Int).Mul(new(big.Int).SetUint64(x), new(big.Int).SetUint64(item.factor))
		if value.Sign() <= 0 || value.Cmp(big.NewInt(MaxMoneyMilli)) > 0 {
			return LoanTerms{}, errors.New("loan component exceeds monetary bound")
		}
		*item.target = value.Int64()
	}
	return t, nil
}

func validateProgressionConfig(values map[string]string) error {
	var tiers []string
	if err := json.Unmarshal([]byte(values["activity_loan_tiers"]), &tiers); err != nil || len(tiers) != 3 {
		return errors.New("loan requires three increasing tiers")
	}
	previous := int64(0)
	for _, tier := range tiers {
		terms, err := CalculateLoanTerms(tier, values["activity_loan_a_milli"], values["activity_loan_b_milli"])
		if err != nil || terms.Principal <= previous {
			return errors.New("invalid loan tier terms")
		}
		previous = terms.Principal
	}
	var stakes []string
	raw := values["game_blackjack_quick_stakes"]
	if err := json.Unmarshal([]byte(raw), &stakes); err != nil || stakes == nil || len(stakes) > 8 {
		return errors.New("invalid blackjack quick stakes")
	}
	minimum, e1 := strconv.ParseInt(values["game_blackjack_min_stake_milli"], 10, 64)
	maximum, e2 := strconv.ParseInt(values["game_blackjack_max_stake_milli"], 10, 64)
	step, e3 := strconv.ParseInt(values["game_blackjack_stake_step_milli"], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || step <= 0 {
		return errors.New("invalid blackjack stake limits")
	}
	previous = 0
	for _, stake := range stakes {
		amount, err := game.ParseAmount(stake)
		if err != nil || game.FormatAmount(amount) != stake || amount < minimum || amount > maximum || (amount-minimum)%step != 0 || amount <= previous {
			return errors.New("quick stakes must be ordered unique valid amounts")
		}
		previous = amount
	}
	canonical, err := json.Marshal(stakes)
	if err != nil || string(canonical) != raw {
		return errors.New("quick stakes must use canonical JSON")
	}
	return nil
}
