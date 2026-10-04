package db

import (
	"math/big"
	"testing"
)

func TestGovernanceActivityIntegerGuardsUseAll128Bits(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	defer database.Close()
	wide := new(big.Int).Lsh(big.NewInt(1), 120)
	wide.Quo(wide, big.NewInt(1000)).Mul(wide, big.NewInt(1000))
	raw := make([]byte, 16)
	wide.FillBytes(raw)
	// A wide exact integer amount remains valid; one milliunit more does not.
	hostileMustExec(t, database, `INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
 VALUES('external','external','sketch_paper',1,?,0,0)`, raw)
	bad := make([]byte, 16)
	new(big.Int).Add(wide, big.NewInt(1)).FillBytes(bad)
	if _, err := database.Exec(`UPDATE credit_accounts SET balance_mag=? WHERE asset_type='sketch_paper'`, bad); err == nil {
		t.Fatal("fractional wide activity balance accepted")
	}
	if _, err := database.Exec(`INSERT INTO credit_accounts(kind,code,asset_type,balance_sign,balance_mag,created_at,updated_at)
 VALUES('platform','image_activity_reserve','general',0,X'00000000000000000000000000000000',0,0)`); err == nil {
		t.Fatal("activity reserve accepted general credits")
	}
}
