package rps

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	deviceCompareInfo = "rps-device-compare/v1"
	ipCompareInfo     = "rps-ip-compare/v1"
	hiddenGestureInfo = "rps-hidden-gesture/v1"
	leaderboardInfo   = "game-leaderboard-tie/v1"
	gestureVersion    = byte(1)
)

type KeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type cryptoKeys struct {
	device      [32]byte
	ip          [32]byte
	gesture     [32]byte
	leaderboard [32]byte
}

func deriveKeys(deriver KeyDeriver) (cryptoKeys, error) {
	if deriver == nil {
		return cryptoKeys{}, ErrInvariant
	}
	var keys cryptoKeys
	for _, item := range []struct {
		info string
		out  *[32]byte
	}{{deviceCompareInfo, &keys.device}, {ipCompareInfo, &keys.ip}, {hiddenGestureInfo, &keys.gesture}, {leaderboardInfo, &keys.leaderboard}} {
		derived, err := deriver.DeriveGenerationTwoSubkey([]byte(item.info))
		if err != nil || len(derived) != 32 {
			clear(derived)
			return cryptoKeys{}, ErrInvariant
		}
		copy(item.out[:], derived)
		clear(derived)
	}
	return keys, nil
}

func rawHMAC(key [32]byte, value []byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(value)
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func hashDeviceToken(key [32]byte, value string) ([32]byte, error) {
	if len(value) != 43 {
		return [32]byte{}, ErrInvalidRequest
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != value {
		clear(raw)
		return [32]byte{}, ErrInvalidRequest
	}
	result := rawHMAC(key, raw)
	clear(raw)
	return result, nil
}

func hashCanonicalIP(key [32]byte, value [16]byte) [32]byte {
	return rawHMAC(key, value[:])
}

func framed(value []byte) []byte {
	result := make([]byte, 4+len(value))
	binary.BigEndian.PutUint32(result[:4], uint32(len(value)))
	copy(result[4:], value)
	return result
}

func gestureAAD(sessionID string, seat int, phaseSeq db.U128, rulesVersion int) []byte {
	fields := [][]byte{
		[]byte(sessionID), []byte(strconv.Itoa(seat)), []byte(phaseSeq.Decimal()), []byte(strconv.Itoa(rulesVersion)),
	}
	result := make([]byte, 0, 96)
	for _, field := range fields {
		result = append(result, framed(field)...)
	}
	return result
}

func sealGesture(random io.Reader, key [32]byte, sessionID string, seat int, phaseSeq db.U128, rulesVersion int, gesture string) ([]byte, error) {
	if random == nil || !db.ValidateOpaqueID(sessionID, "rps_") || seat < 0 || seat > 2 || phaseSeq.Big().Sign() <= 0 || rulesVersion < 1 || !validGesture(gesture) {
		return nil, ErrInvariant
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, ErrInvariant
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvariant
	}
	nonce := make([]byte, gcm.NonceSize())
	if len(nonce) != 12 {
		return nil, ErrInvariant
	}
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, ErrServiceUnavailable
	}
	result := make([]byte, 1, 1+len(nonce)+len(gesture)+gcm.Overhead())
	result[0] = gestureVersion
	result = append(result, nonce...)
	result = gcm.Seal(result, nonce, []byte(gesture), gestureAAD(sessionID, seat, phaseSeq, rulesVersion))
	clear(nonce)
	return result, nil
}

func openGesture(key [32]byte, sessionID string, seat int, phaseSeq db.U128, rulesVersion int, envelope []byte) (string, error) {
	if !db.ValidateOpaqueID(sessionID, "rps_") || seat < 0 || seat > 2 || phaseSeq.Big().Sign() <= 0 || rulesVersion < 1 ||
		len(envelope) < 1+12+16 || len(envelope) > 1+12+8+16 || envelope[0] != gestureVersion {
		return "", ErrInvariant
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", ErrInvariant
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || gcm.NonceSize() != 12 {
		return "", ErrInvariant
	}
	nonce := envelope[1:13]
	plaintext, err := gcm.Open(nil, nonce, envelope[13:], gestureAAD(sessionID, seat, phaseSeq, rulesVersion))
	if err != nil {
		return "", ErrInvariant
	}
	gesture := string(plaintext)
	clear(plaintext)
	if !validGesture(gesture) {
		return "", ErrInvariant
	}
	return gesture, nil
}

func randomIndex(random io.Reader, upper byte) (int, error) {
	if random == nil || upper == 0 {
		return 0, ErrInvariant
	}
	limit := byte(255 - (256 % int(upper)))
	var raw [1]byte
	for {
		if _, err := io.ReadFull(random, raw[:]); err != nil {
			return 0, ErrServiceUnavailable
		}
		if raw[0] <= limit {
			return int(raw[0] % upper), nil
		}
	}
}

func randomGesture(random io.Reader) (string, error) {
	index, err := randomIndex(random, 3)
	if err != nil {
		return "", err
	}
	return []string{GestureRock, GestureScissors, GesturePaper}[index], nil
}

func randomSeatOrder(random io.Reader) ([3]int, error) {
	result := [3]int{0, 1, 2}
	for index := len(result) - 1; index > 0; index-- {
		selected, err := randomIndex(random, byte(index+1))
		if err != nil {
			return [3]int{}, err
		}
		result[index], result[selected] = result[selected], result[index]
	}
	return result, nil
}

func leaderboardTieKey(key [32]byte, board, mode string, userID int64) ([32]byte, error) {
	if board != "profit_rate" && board != "net_profit" || mode != "quick" && mode != "standard" && mode != "deathmatch" || userID <= 0 {
		return [32]byte{}, ErrInvariant
	}
	mac := hmac.New(sha256.New, key[:])
	for _, field := range [][]byte{[]byte("rps"), []byte(board), []byte(mode), []byte(strconv.FormatInt(userID, 10))} {
		_, _ = mac.Write(framed(field))
	}
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result, nil
}
