package db

import (
	"context"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func validateAccountMFAEnvelopes(ctx context.Context, q queryer, codec secret.GenerationTwoContextCodec) error {
	rows, err := q.QueryContext(ctx, "SELECT context_id,encrypted_secret FROM account_mfa_credentials UNION ALL SELECT context_id,encrypted_secret FROM account_mfa_pending")
	if err != nil {
		return err
	}
	defer rows.Close()
	opener, ok := codec.(interface {
		OpenAccountMFA(string, []byte) ([]byte, error)
	})
	for rows.Next() {
		var contextID []byte
		var encrypted string
		if err := rows.Scan(&contextID, &encrypted); err != nil {
			return err
		}
		if !ok || len(contextID) != 16 || len(encrypted) == 0 || len(encrypted) > 256 {
			return errors.New("invalid account MFA material")
		}
		plaintext, err := opener.OpenAccountMFA(encrypted, contextID)
		valid := err == nil && len(plaintext) == 20
		clear(plaintext)
		if !valid {
			return errors.New("invalid account MFA material")
		}
	}
	return rows.Err()
}
