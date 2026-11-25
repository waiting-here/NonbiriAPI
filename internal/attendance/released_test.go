package attendance

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// These tests are built with both releases so the target can replay a request
// against the source's database and compare the complete public response.
const releasedAttendanceKey = "released-attendance-sign-in"

func releasedAttendancePath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("NONBIRI_RELEASED_ATTENDANCE_FIXTURE")
	if path == "" {
		t.Skip("the pinned release gate supplies the source")
	}
	return path
}

func TestWriteReleasedAttendanceFixture(t *testing.T) {
	path := releasedAttendancePath(t)
	if db.CurrentSchemaVersion != 55 && db.CurrentSchemaVersion != 56 && db.CurrentSchemaVersion != 57 && db.CurrentSchemaVersion != 58 {
		t.Fatal("the fixture requires a registered attendance source")
	}
	f := setup(t)
	p := f.create(t, input("cumulative"))
	f.now.Store(p.startsAt)
	result, err := f.s.SignIn(t.Context(), f.user, p.ID, releasedAttendanceKey, SignInInput{SiteDate: p.StartDate})
	if err != nil || result.Period.TotalDays != 1 || len(result.Awards) != 1 || f.balance(t, "general") != "2000" {
		t.Fatal(result, err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path+".json", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	f.exec(t, "VACUUM INTO ?", path)
	if err = os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func releasedAttendanceVault(t *testing.T) *secret.Vault {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vault.Close() })
	return vault
}

func TestReplayReleasedAttendance(t *testing.T) {
	path := releasedAttendancePath(t)
	vault := releasedAttendanceVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	f := fixture{store: store}
	var period, date string
	var signedAt int64
	if err = store.ReadDB().QueryRow("SELECT period_id,user_id,site_date,signed_at FROM attendance_sign_ins").Scan(&period, &f.user, &date, &signedAt); err != nil {
		t.Fatal(err)
	}
	auth := &authority{}
	s, err := New(Config{Database: store.DB(), ReadDatabase: store.ReadDB(), Users: auth, Admins: auth, Gate: auth, Keys: vault, Now: func() time.Time { return time.Unix(signedAt, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SignIn(t.Context(), f.user, period, releasedAttendanceKey, SignInInput{SiteDate: date})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path + ".json")
	if err != nil || !bytes.Equal(raw, want) || f.balance(t, "general") != "2000" {
		t.Fatal("released receipt, progress, tiers or balance changed on replay", string(raw), err)
	}
}

func TestReleasedAttendanceRejectsUpgradedSchema(t *testing.T) {
	path := releasedAttendancePath(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(path, releasedAttendanceVault(t))
	if store != nil {
		store.Close()
		t.Fatal("released application opened the upgraded database")
	}
	var mismatch *db.StartupError
	if !errors.As(err, &mismatch) || mismatch.Kind != db.StartupSchemaMismatch || mismatch.ExpectedSchemaVersion != db.CurrentSchemaVersion || mismatch.ActualSchemaVersion == nil || *mismatch.ActualSchemaVersion != 59 {
		t.Fatal("wrong rejection", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejection modified the upgraded database", err)
	}
}
