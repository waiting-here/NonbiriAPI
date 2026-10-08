package adminalerts

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/blacklist"
)

func TestDeletionBlacklistNotePreservesCapturedReasonAndOtherPenalties(t *testing.T) {
	const penalty = "试图通过删号逃避处罚"
	const debt = "\n试图通过删号逃避负债"
	for _, tc := range []struct {
		name, reason, want string
		ban, pause, debt   bool
		nilReason          bool
	}{
		{name: "missing", ban: true, nilReason: true, want: penalty},
		{name: "empty", ban: true, want: penalty},
		{name: "blank", ban: true, reason: " \t\r\n", want: penalty},
		{name: "multiline", ban: true, reason: " Original\r\nsecond\rthird\t<&> ", want: penalty + "\n封禁原因： Original\nsecond\nthird\t<&> "},
		{name: "ban_and_debt", ban: true, debt: true, reason: "Original", want: penalty + "\n封禁原因：Original" + debt},
		{name: "empty_ban_and_debt", ban: true, debt: true, want: penalty + debt},
		{name: "expired_ban_active_pause", pause: true, reason: "Expired", want: "删号时仍有生效中的处罚。"},
		{name: "expired_ban_debt", debt: true, reason: "Expired", want: "删号时仍有未结清的积分负债。"},
		{name: "expired_ban_pause_and_debt", pause: true, debt: true, reason: "Expired", want: "删号时仍有生效中的处罚，且有积分负债。"},
		{name: "maximum_unicode", ban: true, debt: true, reason: strings.Repeat("🐟", 1024), want: penalty + "\n封禁原因：" + strings.Repeat("🐟", 1024) + debt},
		{name: "maximum_escaped", ban: true, reason: strings.Repeat("<", 1024), want: penalty + "\n封禁原因：" + strings.Repeat("<", 1024)},
		{name: "old_invalid_text", ban: true, reason: "before\x00\xff\x7f\u0085after", want: penalty + "\n封禁原因：beforeafter"},
		{name: "old_unusable_text", ban: true, reason: "\x00\xff", want: penalty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := &DeletionBefore{
				Ban:          DeletionPenalty{ActiveAtDeletion: &tc.ban},
				CharityPause: DeletionPenalty{ActiveAtDeletion: &tc.pause},
			}
			if !tc.nilReason {
				before.Ban.Reason = &tc.reason
			}
			original := tc.reason
			got := deletionBlacklistNote(before, tc.debt)
			if got != tc.want {
				t.Fatalf("note=%q want=%q", got, tc.want)
			}
			if normalized, err := blacklist.NormalizeNote(got); err != nil || normalized != got {
				t.Fatalf("note outside blacklist contract: %q / %v", normalized, err)
			}
			if tc.reason != original || (before.Ban.Reason == nil) != tc.nilReason {
				t.Fatal("changed the captured original")
			}
		})
	}
}

func TestDeletionBlacklistNoteBoundsOldTextWithoutLosingDebt(t *testing.T) {
	active, inactive := true, false
	reason := strings.Repeat("🐟", 2100)
	before := &DeletionBefore{
		Ban:          DeletionPenalty{ActiveAtDeletion: &active, Reason: &reason},
		CharityPause: DeletionPenalty{ActiveAtDeletion: &inactive},
	}
	got := deletionBlacklistNote(before, true)
	if utf8.RuneCountInString(got) != 2000 || !strings.HasPrefix(got, "试图通过删号逃避处罚\n封禁原因：🐟") || !strings.HasSuffix(got, "…\n试图通过删号逃避负债") {
		t.Fatal("old reason lost its bounds or penalty context")
	}
	if _, err := blacklist.NormalizeNote(got); err != nil {
		t.Fatal(err)
	}
	if *before.Ban.Reason != strings.Repeat("🐟", 2100) {
		t.Fatal("bounded the original snapshot instead of its note")
	}
}
