package db

import (
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/endpointtags"
	"modernc.org/sqlite"
)

//go:embed endpoint_tag_rules.sql
var endpointTagRulesSchema string

// The published six-label rebuild remains unchanged for older upgrade steps.
var EndpointTagRulesSQL = strings.Replace(EndpointTagsSQL, "'invalid_entry'", "'invalid_entry','mainstream','mysterious','other_third_party'", 1) + endpointTagRulesSchema

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("nbi_endpoint_domains", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, ok := args[0].(string)
		if !ok {
			return "[]", nil
		}
		encoded, err := json.Marshal(endpointtags.Domains(value))
		return string(encoded), err
	})
}
