package app

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func runMaintenance(args []string, output io.Writer) error {
	if len(args) >= 2 && args[0] == "maintenance" {
		switch args[1] {
		case "verify":
			return runVerification(args[2:], output)
		case "schema-info":
			if len(args) == 3 && args[2] == "--json" {
				return json.NewEncoder(output).Encode(db.SchemaDescriptor())
			}
		}
	}
	return errors.New("usage: nonbiriapi maintenance verify; nonbiriapi maintenance schema-info --json")
}
