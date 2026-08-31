package activities

import (
	"errors"
	"testing"
	"time"
)

func TestBeijingThursdayWindowAndExclusiveDeadline(t *testing.T) {
	opensAt := beijingThursday(2027, 2, 18)
	if err := validateThursdayWindow("2027-02-18", opensAt, opensAt-1); err != nil {
		t.Fatalf("valid Beijing Thursday: %v", err)
	}
	if err := validateThursdayWindow("2027-02-17", opensAt-86400, opensAt-2); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Wednesday error=%v", err)
	}
	if err := validateThursdayWindow("2027-02-18", opensAt, opensAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("open-time configuration error=%v", err)
	}
	if !naturalThursdayOpen(opensAt, opensAt, opensAt+86400) || naturalThursdayOpen(opensAt+86400, opensAt, opensAt+86400) {
		t.Fatal("Thursday deadline is not [open, close)")
	}
	if !resultVisible(opensAt+86400, opensAt) || !resultVisible(opensAt+7*86400-1, opensAt) ||
		resultVisible(opensAt+7*86400, opensAt) {
		t.Fatal("Thursday result is not limited to [close,next natural open)")
	}
	utc := time.Unix(opensAt, 0).UTC()
	if utc.Hour() != 16 || utc.Weekday() != time.Wednesday {
		t.Fatalf("Beijing midnight UTC projection=%s", utc)
	}
}
