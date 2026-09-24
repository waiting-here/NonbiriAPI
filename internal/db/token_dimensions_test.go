package db

import (
	"context"
	"testing"
)

func TestPublishedTokenQuotaUpgradePreservesScalarHistory(t *testing.T) {
	database := governanceSourceFixture(t)
	donation := hostileInsertDonation(t, database, nil)
	result := hostileMustExec(t, database, `INSERT INTO donation_keys(donation_id,source_endpoint_key_id,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,token_limit_mag,token_reserve,failure_streak,streak_generation,next_claim_seq,next_fold_seq,enabled,created_at,updated_at,ended_at,ended_reason,report_match_until,report_fingerprint)
VALUES(?,1,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),?,?,?,5,zeroblob(16),?,zeroblob(16),zeroblob(16),0,1,1,1,'terminated',7776001,zeroblob(32))`, donation, hostileBlob16(7), hostileBlob16(3), hostileBlob16(20), hostileBlob16(1))
	key := hostileMustLastID(t, result)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var total, reserved, limit, input, output, unknown []byte
	var started int64
	var inputLimit, outputLimit []byte
	if err := database.QueryRow(`SELECT tokens_used,tokens_reserved,token_limit_mag,input_tokens_used,output_tokens_used,unattributed_total_tokens,breakdown_started_at,input_token_limit_mag,output_token_limit_mag FROM donation_keys WHERE id=?`, key).
		Scan(&total, &reserved, &limit, &input, &output, &unknown, &started, &inputLimit, &outputLimit); err != nil {
		t.Fatal(err)
	}
	for n, raw := range [][]byte{total, reserved, limit, input, output, unknown} {
		value, err := DecodeU128(raw)
		if err != nil || value.Decimal() != []string{"7", "3", "20", "0", "0", "7"}[n] {
			t.Fatal(n, value, err)
		}
	}
	if started <= 1 || inputLimit != nil || outputLimit != nil {
		t.Fatal(started, inputLimit, outputLimit)
	}
	for _, query := range []string{
		`UPDATE donation_keys SET input_token_reserve=1`,
		`UPDATE donation_keys SET input_token_reserve=0,output_token_reserve=0`,
		`UPDATE donation_keys SET input_token_reserve=9223372036854775807,output_token_reserve=1`,
		`UPDATE donation_keys SET input_token_limit_mag=zeroblob(16)`,
		`UPDATE donation_keys SET input_token_reserve=1,output_token_reserve=1,input_token_limit_mag=X'00000000000000008000000000000000'`,
	} {
		if _, err := database.Exec(query); err == nil {
			t.Fatal("invalid dimension configuration accepted", query)
		}
	}
	hostileMustExec(t, database, `UPDATE donation_keys SET input_token_reserve=9223372036854775806,output_token_reserve=1,input_token_limit_mag=X'00000000000000007FFFFFFFFFFFFFFF' WHERE id=?`, key)
}
