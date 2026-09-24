package db

import (
	"fmt"
	"strings"
)

func tokenDimensionsSchema() string {
	var b strings.Builder
	for _, column := range []string{"input_token_limit_mag", "output_token_limit_mag"} {
		fmt.Fprintf(&b, "ALTER TABLE donation_keys ADD COLUMN %s BLOB CHECK(%s IS NULL OR (typeof(%s)='blob' AND length(%s)=16 AND hex(%s)<='00000000000000007FFFFFFFFFFFFFFF'));\n", column, column, column, column, column)
	}
	for _, column := range []string{"input_token_reserve", "output_token_reserve"} {
		fmt.Fprintf(&b, "ALTER TABLE donation_keys ADD COLUMN %s INTEGER CHECK(%s IS NULL OR (typeof(%s)='integer' AND %s>=0));\n", column, column, column, column)
	}
	for _, column := range []string{"input_tokens_used", "output_tokens_used", "input_tokens_reserved", "output_tokens_reserved", "unattributed_total_tokens"} {
		fmt.Fprintf(&b, "ALTER TABLE donation_keys ADD COLUMN %s BLOB NOT NULL DEFAULT X'00000000000000000000000000000000' CHECK(typeof(%s)='blob' AND length(%s)=16);\n", column, column, column)
	}
	b.WriteString(`ALTER TABLE donation_keys ADD COLUMN breakdown_started_at INTEGER NOT NULL DEFAULT 0 CHECK(typeof(breakdown_started_at)='integer' AND breakdown_started_at BETWEEN 0 AND 253402300799);
CREATE TRIGGER donation_token_breakdown_insert AFTER INSERT ON donation_keys WHEN NEW.breakdown_started_at=0
BEGIN UPDATE donation_keys SET breakdown_started_at=NEW.created_at WHERE id=NEW.id; END;
`)
	for _, table := range []string{"dispatch_claims", "donation_usage_reservations"} {
		for _, dimension := range []string{"input", "output"} {
			column := "reserved_" + dimension + "_tokens"
			if table == "donation_usage_reservations" {
				column = dimension + "_tokens_reserved"
			}
			fmt.Fprintf(&b, "ALTER TABLE %s ADD COLUMN %s INTEGER CHECK(%s IS NULL OR (typeof(%s)='integer' AND %s>=0));\n", table, column, column, column, column)
		}
	}
	for _, column := range []string{"input_tokens_actual", "output_tokens_actual"} {
		fmt.Fprintf(&b, "ALTER TABLE donation_usage_reservations ADD COLUMN %s INTEGER CHECK(%s IS NULL OR (typeof(%s)='integer' AND %s>=0));\n", column, column, column, column)
	}
	for _, event := range []string{"INSERT", "UPDATE"} {
		fmt.Fprintf(&b, `CREATE TRIGGER donation_token_configuration_%s BEFORE %s ON donation_keys
WHEN (NEW.input_token_reserve IS NULL)<>(NEW.output_token_reserve IS NULL)
 OR (NEW.input_token_reserve IS NOT NULL AND (NEW.input_token_reserve>9223372036854775807-NEW.output_token_reserve OR (NEW.input_token_reserve=0 AND NEW.output_token_reserve=0)))
 OR ((NEW.input_token_limit_mag IS NOT NULL OR NEW.output_token_limit_mag IS NOT NULL) AND NEW.input_token_reserve IS NULL)
 OR (NEW.input_token_reserve IS NULL AND EXISTS(SELECT 1 FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id AND e.epoch=r.current_epoch WHERE r.donation_key_id=NEW.id AND e.metric IN ('input_tokens','output_tokens')))
BEGIN SELECT RAISE(ABORT,'invalid token reservation configuration'); END;
`, strings.ToLower(event), event)
		for _, table := range []string{"dispatch_claims", "donation_usage_reservations"} {
			i, o, total := "reserved_input_tokens", "reserved_output_tokens", "reserved_tokens"
			if table == "donation_usage_reservations" {
				i, o, total = "input_tokens_reserved", "output_tokens_reserved", "tokens_reserved"
			}
			fmt.Fprintf(&b, `CREATE TRIGGER %s_token_vector_%s BEFORE %s ON %s
WHEN (NEW.%s IS NULL)<>(NEW.%s IS NULL)
 OR (NEW.%s IS NOT NULL AND (NEW.%s>9223372036854775807-NEW.%s OR NEW.%s+NEW.%s<>NEW.%s))
BEGIN SELECT RAISE(ABORT,'invalid token reservation vector'); END;
`, table, strings.ToLower(event), event, table, i, o, i, i, o, i, o, total)
		}
		fmt.Fprintf(&b, `CREATE TRIGGER donation_token_actual_%s BEFORE %s ON donation_usage_reservations
WHEN (NEW.input_tokens_actual IS NULL)<>(NEW.output_tokens_actual IS NULL)
 OR (NEW.input_tokens_actual IS NOT NULL AND (NEW.tokens_actual IS NULL OR NEW.input_tokens_actual>9223372036854775807-NEW.output_tokens_actual OR NEW.input_tokens_actual+NEW.output_tokens_actual<>NEW.tokens_actual))
BEGIN SELECT RAISE(ABORT,'invalid actual token vector'); END;
`, strings.ToLower(event), event)
	}
	return b.String()
}
