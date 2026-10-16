// Package totp implements the six-digit SHA-1 profile of RFC 6238.
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
)

// Code returns the RFC 4226 dynamic-truncation value for a time step.
func Code(seed []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, seed)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	clear(digest)
	return fmt.Sprintf("%06d", value%1000000)
}

// Match accepts only canonical codes in the current 30-second step and its
// immediate neighbors. A previously consumed or older step is rejected.
func Match(seed []byte, code string, now, lastStep int64) (int64, bool) {
	if len(seed) != 20 || len(code) != 6 || now < 0 {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	match := int64(-1)
	for _, offset := range []int64{-1, 0, 1} {
		step := now/30 + offset
		if step < 0 {
			continue
		}
		equal := subtle.ConstantTimeCompare([]byte(Code(seed, step)), []byte(code))
		if equal == 1 && step > lastStep && step > match {
			match = step
		}
	}
	return match, match >= 0
}
