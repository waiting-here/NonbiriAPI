package anthropic

import (
	"crypto/sha256"
	"testing"
)

func TestSensitiveGuardCloneAndClearKeyedFingerprint(t *testing.T) {
	guard := newSensitiveGuard([]byte("sk-sensitive"))
	if len(guard.detectors) != 1 {
		t.Fatalf("detectors=%d", len(guard.detectors))
	}
	zeroKey := [sha256.Size]byte{}
	if guard.fingerprintKey == zeroKey {
		t.Fatal("fingerprint key was not initialized")
	}

	clone := guard.clone()
	if clone.fingerprintKey != guard.fingerprintKey {
		t.Fatal("clone did not preserve the fingerprint key")
	}
	if clone.Contains([]byte("prefix-sk-")) || !clone.Contains([]byte("sensitive-suffix")) {
		t.Fatal("clone did not preserve split exact-match detection")
	}

	detector := guard.detectors[0]
	guard.Clear()
	if guard.fingerprintKey != zeroKey || guard.detectors != nil {
		t.Fatal("guard fingerprint state was not cleared")
	}
	if detector.digest != zeroKey || detector.window != nil || detector.hasher != nil {
		t.Fatal("detector fingerprint state was not cleared")
	}
	clone.Clear()
}
