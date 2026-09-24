package imageactivity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

type cursorData struct{ Scope, After string }

func (s *Service) cursor(scope, after string) (string, error) {
	key, err := s.config.Vault.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/image-page-cursor/v1"))
	if err != nil {
		return "", err
	}
	defer clear(key)
	raw, _ := json.Marshal(cursorData{scope, after})
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *Service) readCursor(raw, scope string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > 2048 {
		return "", ErrInvalid
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return "", ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrInvalid
	}
	tag, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrInvalid
	}
	key, err := s.config.Vault.DeriveGenerationTwoSubkey([]byte("NonbiriAPI/image-page-cursor/v1"))
	if err != nil {
		return "", err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(body)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return "", ErrInvalid
	}
	var data cursorData
	if json.Unmarshal(body, &data) != nil || data.Scope != scope || len(data.After) > 128 {
		return "", ErrInvalid
	}
	return data.After, nil
}
func pageLimit(limit int) bool { return limit >= 1 && limit <= 100 }
func scope(kind string, user int64, extra string) string {
	return kind + ":" + strconv.FormatInt(user, 10) + ":" + extra
}
func (s *Service) adminRead(ctx context.Context, user int64) error {
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.config.Admins.AuthorizeAdmin(ctx, tx, user); err != nil {
		return err
	}
	return tx.Commit()
}
