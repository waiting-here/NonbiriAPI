package charityaccess

import (
	"strings"
	"testing"
)

func TestEveryLevelSubsetHasExactMembership(t *testing.T) {
	for subset := 0; subset < 64; subset++ {
		levels, err := Levels(subset)
		if err != nil || levels == nil {
			t.Fatalf("subset %d: %v", subset, err)
		}
		mask, err := Mask(levels)
		if err != nil || mask != subset {
			t.Fatalf("subset %d became %d: %v", subset, mask, err)
		}
		previous := 0
		for _, level := range levels {
			if level <= previous {
				t.Fatal("levels are not ordered")
			}
			previous = level
		}
		for level := 0; level <= 7; level++ {
			want := false
			for _, member := range levels {
				want = want || member == level
			}
			if Allows(mask, level) != want {
				t.Fatalf("subset %d level %d membership differs", subset, level)
			}
		}
	}
	for _, levels := range [][]int{{0}, {7}, {-1}, {1, 1}, {1, 2, 3, 4, 5, 5}} {
		if _, err := Mask(levels); err == nil {
			t.Fatalf("invalid set accepted: %v", levels)
		}
	}
	for _, mask := range []int{-1, 64, 255} {
		if _, err := Levels(mask); err == nil || Allows(mask, 1) {
			t.Fatalf("invalid mask accepted: %d", mask)
		}
	}
}

func TestPublicDescriptionUsesPlainTextAndUnicodeBounds(t *testing.T) {
	for _, value := range []string{"", "<b>plain</b> https://example.invalid\n\t", strings.Repeat("😀", 1024)} {
		if got, err := NormalizeDescription(value); err != nil || got != value {
			t.Fatalf("valid description changed: %v", err)
		}
	}
	if got, err := NormalizeDescription("a\r\nb\n"); err != nil || got != "a\nb\n" {
		t.Fatalf("CRLF: %q %v", got, err)
	}
	for _, value := range []string{"\x00", "\r", "\x7f", "\u0085", "\xff", strings.Repeat("a", 1025), strings.Repeat("😀", 1025)} {
		if _, err := NormalizeDescription(value); err == nil {
			t.Fatalf("invalid description accepted (%d bytes)", len(value))
		}
	}
}
