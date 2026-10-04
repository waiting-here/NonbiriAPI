package db

import (
	"testing"
)

func TestLoanTermsExactArithmeticAndBounds(t *testing.T) {
	got, err := CalculateLoanTerms("10000", "900", "1300")
	if err != nil || got.Nominal != 10000000 || got.Disbursed != 9000000 || got.Fee != 1000000 || got.Repayment != 13000000 || got.Interest != 3000000 {
		t.Fatal(got, err)
	}
	for _, values := range [][3]string{{"0", "900", "1300"}, {"1e4", "900", "1300"}, {"10000", "1000", "1300"}, {"10000", "900", "1000"}, {"9000000000000", "900", "1300"}, {"10000", "900", "99999999999999999999999999999999"}} {
		if _, err := CalculateLoanTerms(values[0], values[1], values[2]); err == nil {
			t.Fatal("invalid terms accepted", values)
		}
	}
}
