package db

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"math/big"
	"testing"
	"time"
)

func browseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestBrowseSQLWideCountersKeepExactCapacityAndOverflow(t *testing.T) {
	database := browseTestDB(t)
	wide, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	wide128, _ := U128FromBig(wide)
	blob := EncodeU128(wide128)
	var remaining, sum, empty []byte
	if err := database.QueryRow(`SELECT nbi_u128_remaining(?,nbi_u128(5),nbi_u128(7),nbi_u128(11))`, blob).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(remaining).String() != "123456789012345678901234567867" {
		t.Fatalf("wide subtraction rounded: %x", remaining)
	}
	if err := database.QueryRow(`WITH values_to_sum(x) AS (SELECT ? UNION ALL SELECT nbi_u128(19)) SELECT nbi_u128_sum(x) FROM values_to_sum`, blob).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(sum).String() != "123456789012345678901234567909" {
		t.Fatalf("wide aggregate rounded: %x", sum)
	}
	if err := database.QueryRow(`SELECT nbi_u128_sum(nbi_u128(0)) WHERE 0`).Scan(&empty); err != nil || len(empty) != 16 || new(big.Int).SetBytes(empty).Sign() != 0 {
		t.Fatalf("empty aggregate: %x, %v", empty, err)
	}
	var full, spent int
	if err := database.QueryRow(`SELECT
nbi_u128_remaining(NULL,nbi_u128(7),nbi_u128(8),nbi_u128(9))>nbi_u128(100),
nbi_u128_remaining(nbi_u128(2),nbi_u128(3),nbi_u128(4),nbi_u128(5))=nbi_u128(0)`).Scan(&full, &spent); err != nil || full != 1 || spent != 1 {
		t.Fatalf("unlimited/exhausted capacity: %d/%d, %v", full, spent, err)
	}
	for _, query := range []string{
		`SELECT nbi_u128(-1)`, `SELECT nbi_u128(1.0)`, `SELECT nbi_u128('1')`,
		`SELECT nbi_u128_remaining(nbi_u128(1),'bad',nbi_u128(0),nbi_u128(0))`,
		`SELECT nbi_u128_sum(NULL)`,
	} {
		if err := database.QueryRow(query).Scan(&remaining); err == nil {
			t.Fatalf("invalid SQL scalar accepted: %s", query)
		}
	}
	if err := database.QueryRow(`WITH values_to_sum(x) AS (SELECT ? UNION ALL SELECT nbi_u128(1)) SELECT nbi_u128_sum(x) FROM values_to_sum`, bytes.Repeat([]byte{255}, 16)).Scan(&sum); err == nil {
		t.Fatal("wide sum silently overflowed")
	}
}

func TestBrowseSQLCalendarAndOpaqueSourceIdentity(t *testing.T) {
	database := browseTestDB(t)
	instant := time.Date(2027, 3, 15, 6, 30, 0, 0, time.UTC).Unix()
	var left, start int64
	if err := database.QueryRow(`SELECT nbi_calendar_subtract(?,'day','America/New_York'),nbi_calendar_start(?,'day','America/New_York',0)`, instant, instant).Scan(&left, &start); err != nil {
		t.Fatal(err)
	}
	if left != time.Date(2027, 3, 14, 7, 30, 0, 0, time.UTC).Unix() || start != time.Date(2027, 3, 15, 4, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("SQL calendar lost gap resolution: %d, %d", left, start)
	}
	for _, query := range []string{
		`SELECT nbi_calendar_start(0,'week','UTC',0)`, `SELECT nbi_calendar_subtract(0,'day','missing')`,
		`SELECT nbi_calendar_subtract('0','day','UTC')`,
	} {
		if err := database.QueryRow(query).Scan(&left); err == nil {
			t.Fatalf("invalid calendar scalar accepted: %s", query)
		}
	}
	var original, renamed, second, custom, otherConnector string
	if err := database.QueryRow(`SELECT nbi_donation_source('stable-channel','openai-compatible','https://before.example/v1'),
nbi_donation_source('stable-channel','anthropic-compatible','https://after.example/v1'),
nbi_donation_source('another-channel','openai-compatible','https://before.example/v1'),
nbi_donation_source(NULL,'openai-compatible','https://before.example/v1'),
nbi_donation_source(NULL,'anthropic-compatible','https://before.example/v1')`).Scan(&original, &renamed, &second, &custom, &otherConnector); err != nil {
		t.Fatal(err)
	}
	if original != renamed || original == second || original == custom || custom == otherConnector || len(custom) != 47 || custom[:4] != "dsg_" {
		t.Fatalf("unstable or ambiguous source identities: %q %q %q %q %q", original, renamed, second, custom, otherConnector)
	}
	digest, err := base64.RawURLEncoding.DecodeString(custom[4:])
	if err != nil || len(digest) != 32 || base64.RawURLEncoding.EncodeToString(digest) != custom[4:] {
		t.Fatalf("non-canonical source digest: %q, %v", custom, err)
	}
}
