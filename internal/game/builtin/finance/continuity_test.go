package finance

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
)

type financeContinuityDeriver struct{}

func (financeContinuityDeriver) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	key := sha256.Sum256(append([]byte("finance-test:"), info...))
	return key[:], nil
}

func bindFinanceUser(t *testing.T, database *sql.DB, tx *sql.Tx, userID int64) {
	t.Helper()
	service, err := continuity.New(database, financeContinuityDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.BindUserTx(context.Background(), tx, userID); err != nil {
		t.Fatal(err)
	}
}
