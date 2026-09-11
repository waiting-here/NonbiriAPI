package linklink

import (
	"testing"

	builtinfinance "github.com/waiting-here/NonbiriAPI/internal/game/builtin/finance"
)

func registeredFinance(t *testing.T, id string) builtinfinance.Capabilities {
	t.Helper()
	value, err := builtinfinance.ForModule(id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
