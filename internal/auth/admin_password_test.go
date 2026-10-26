package auth

import (
	"strings"
	"testing"
)

func TestAdminPasswordVerifier(t *testing.T) {
	const password = "a long password with Unicode: 密码"
	first := newAdminPasswordVerifier(password)
	second := newAdminPasswordVerifier(password)
	if first.key == second.key || first.mac == second.mac {
		t.Fatal("password verifier must have a fresh runtime key")
	}
	for _, candidate := range []string{password, "", password + " ", password[:len(password)-1], "wrong", strings.Repeat("x", maxPasswordBytes+1)} {
		want := candidate == password
		if first.matches(candidate) != want || second.matches(candidate) != want {
			t.Fatalf("password match differs from exact comparison for %q", candidate)
		}
	}
	boundary := strings.Repeat("x", maxPasswordBytes)
	long := newAdminPasswordVerifier(boundary)
	if !long.matches(boundary) || long.matches(boundary+"x") {
		t.Fatal("password length boundary changed")
	}
}
