package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxMoneyMilli        = int64(9_000_000_000_000_000)
	maxOpaqueIDBody      = 22
	maxCursorBytes       = 512
	cursorVersion   byte = 1
	maxCursorExpiry      = uint64(253402300799)

	credentialReportSourceIPEnvelopeVersion byte = 1
	credentialReportSourceIPEnvelopeSize         = 1 + 12 + 16 + 16

	// Exported wire constants let persistence callers assert the exact frozen
	// envelope shape without duplicating its arithmetic.
	CredentialReportSourceIPEnvelopeVersion = credentialReportSourceIPEnvelopeVersion
	CredentialReportSourceIPEnvelopeSize    = credentialReportSourceIPEnvelopeSize
)

var (
	ErrInvalidWideScalar       = errors.New("invalid wide scalar")
	ErrInvalidOpaqueID         = errors.New("invalid opaque id")
	ErrInvalidCursor           = errors.New("invalid pagination cursor")
	ErrInvalidSourceIPEnvelope = errors.New("invalid credential report source ip envelope")
)

// U128 and U256 are fixed-width unsigned big-endian wire/database scalars.
// They intentionally do not implement json.Number: the HTTP layer projects
// them to canonical decimal strings.
type U128 [16]byte
type U256 [32]byte

func DecodeU128(value []byte) (U128, error) {
	var out U128
	if len(value) != len(out) {
		return out, ErrInvalidWideScalar
	}
	copy(out[:], value)
	return out, nil
}

func EncodeU128(value U128) []byte {
	out := make([]byte, len(value))
	copy(out, value[:])
	return out
}

func DecodeU256(value []byte) (U256, error) {
	var out U256
	if len(value) != len(out) {
		return out, ErrInvalidWideScalar
	}
	copy(out[:], value)
	return out, nil
}

func EncodeU256(value U256) []byte {
	out := make([]byte, len(value))
	copy(out, value[:])
	return out
}

func U128FromBig(value *big.Int) (U128, error) {
	var out U128
	if value == nil || value.Sign() < 0 || value.BitLen() > 128 {
		return out, ErrInvalidWideScalar
	}
	value.FillBytes(out[:])
	return out, nil
}

func U256FromBig(value *big.Int) (U256, error) {
	var out U256
	if value == nil || value.Sign() < 0 || value.BitLen() > 256 {
		return out, ErrInvalidWideScalar
	}
	value.FillBytes(out[:])
	return out, nil
}

func (value U128) Big() *big.Int   { return new(big.Int).SetBytes(value[:]) }
func (value U256) Big() *big.Int   { return new(big.Int).SetBytes(value[:]) }
func (value U128) Decimal() string { return value.Big().String() }
func (value U256) Decimal() string { return value.Big().String() }

func ParseU128Decimal(text string) (U128, error) {
	var out U128
	if !validUnsignedDecimal(text) {
		return out, ErrInvalidWideScalar
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return out, ErrInvalidWideScalar
	}
	return U128FromBig(n)
}

func ParseU256Decimal(text string) (U256, error) {
	var out U256
	if !validUnsignedDecimal(text) {
		return out, ErrInvalidWideScalar
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return out, ErrInvalidWideScalar
	}
	return U256FromBig(n)
}

// SM128 uses a separate sign and magnitude. Sign is -1, 0, or +1; zero is
// represented only by a zero magnitude and sign zero, and the high bit of the
// magnitude is reserved so its absolute value is always <2^127.
type SM128 struct {
	Sign int8
	Mag  U128
}

func ValidateSM128(sign int, magnitude []byte) error {
	if sign < -1 || sign > 1 || len(magnitude) != 16 || magnitude[0]&0x80 != 0 {
		return ErrInvalidWideScalar
	}
	zero := true
	for _, b := range magnitude {
		if b != 0 {
			zero = false
			break
		}
	}
	if (sign == 0) != zero {
		return ErrInvalidWideScalar
	}
	return nil
}

func NewSM128(sign int, magnitude []byte) (SM128, error) {
	var out SM128
	if err := ValidateSM128(sign, magnitude); err != nil {
		return out, err
	}
	out.Sign = int8(sign)
	copy(out.Mag[:], magnitude)
	return out, nil
}

func EncodeSM128(value SM128) ([]byte, error) {
	if err := ValidateSM128(int(value.Sign), value.Mag[:]); err != nil {
		return nil, err
	}
	out := make([]byte, 17)
	out[0] = byte(value.Sign + 1)
	copy(out[1:], value.Mag[:])
	return out, nil
}

func DecodeSM128(value []byte) (SM128, error) {
	var out SM128
	if len(value) != 17 || value[0] > 2 {
		return out, ErrInvalidWideScalar
	}
	out.Sign = int8(value[0]) - 1
	copy(out.Mag[:], value[1:])
	if err := ValidateSM128(int(out.Sign), out.Mag[:]); err != nil {
		return SM128{}, err
	}
	return out, nil
}

func SM128FromBig(value *big.Int) (SM128, error) {
	var out SM128
	if value == nil || value.BitLen() > 127 {
		return out, ErrInvalidWideScalar
	}
	if value.Sign() == 0 {
		return out, nil
	}
	out.Sign = int8(value.Sign())
	mag := new(big.Int).Abs(value)
	if _, err := U128FromBig(mag); err != nil {
		return SM128{}, err
	}
	mag.FillBytes(out.Mag[:])
	return out, nil
}

func (value SM128) Big() *big.Int {
	out := value.Mag.Big()
	if value.Sign < 0 {
		out.Neg(out)
	}
	return out
}

func (value SM128) Decimal() string { return value.Big().String() }

func ParseSM128Decimal(text string) (SM128, error) {
	if !validSignedDecimal(text) {
		return SM128{}, ErrInvalidWideScalar
	}
	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return SM128{}, ErrInvalidWideScalar
	}
	return SM128FromBig(n)
}

func validSignedDecimal(text string) bool {
	if text == "" {
		return false
	}
	if text[0] == '-' {
		// Negative zero and signed leading-zero forms are not canonical.
		return len(text) > 1 && text[1] >= '1' && text[1] <= '9' && validUnsignedDecimal(text[1:])
	}
	return validUnsignedDecimal(text)
}

func validUnsignedDecimal(text string) bool {
	if text == "" || (len(text) > 1 && text[0] == '0') {
		return false
	}
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// GenerateOpaqueID creates a canonical random 128-bit identifier. The
// prefix must be one of the frozen Generation 2 namespaces.
func GenerateOpaqueID(prefix string) (string, error) {
	if !validOpaquePrefix(prefix) {
		return "", ErrInvalidOpaqueID
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func ValidateOpaqueID(value, prefix string) bool {
	if !validOpaquePrefix(prefix) || !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+maxOpaqueIDBody {
		return false
	}
	body := value[len(prefix):]
	for i := 0; i < len(body); i++ {
		c := body[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	if body[len(body)-1] != 'A' && body[len(body)-1] != 'Q' && body[len(body)-1] != 'g' && body[len(body)-1] != 'w' {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	return err == nil && len(raw) == 16 && base64.RawURLEncoding.EncodeToString(raw) == body
}

func validOpaquePrefix(prefix string) bool {
	switch prefix {
	case "ann_", "op_", "req_", "clm_", "pol_", "thu_", "fb_", "ll_", "rpsq_", "rps_", "rpc_", "rpt_", "iss_", "lgh_", "b1e_", "sse_", "thp_", "gle_", "dbs_", "dbt_", "dbe_", "mch_":
		return true
	default:
		return false
	}
}

// HKDFSHA256 is a small RFC 5869 implementation kept local to the schema
// package so cursor/fingerprint derivation has one audited primitive.
func HKDFSHA256(ikm, salt, info []byte, length int) ([]byte, error) {
	if length < 0 || length > 255*sha256.Size {
		return nil, ErrInvalidWideScalar
	}
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(ikm)
	prk := mac.Sum(nil)
	defer clear(prk)
	result := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(result) < length; counter++ {
		mac = hmac.New(sha256.New, prk)
		_, _ = mac.Write(previous)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		result = append(result, previous...)
	}
	clear(previous)
	return result[:length], nil
}

func DeriveGenerationTwoKey(masterKey, info []byte) ([]byte, error) {
	if len(masterKey) != 32 || len(info) == 0 || len(info) > 256 {
		return nil, ErrInvalidWideScalar
	}
	return HKDFSHA256(masterKey, []byte("NonbiriAPI/generation/2"), info, 32)
}

const (
	credentialReportFingerprintInfo = "credential-report-fingerprint/v1"
	credentialReportIdempotencyInfo = "credential-report-idempotency/v1"
	credentialReportTargetInfo      = "credential-report-target-ref/v1"
	credentialReportSourceIPInfo    = "credential-report-source-ip/v1"
	credentialReportRateInfo        = "credential-report-rate-scope/v1"
	gameLeaderboardTieInfo          = "game-leaderboard-tie/v1"
)

// generationTwoField is the frozen F/FB encoding used by all report and
// ranking digests. It prefixes byte length (not rune count), so concatenated
// fields are unambiguous and Unicode is preserved exactly as received.
func generationTwoField(value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	out := make([]byte, 0, len(length)+len(value))
	out = append(out, length[:]...)
	return append(out, value...)
}

func generationTwoHMAC(masterKey []byte, info string, fields ...[]byte) ([32]byte, error) {
	var out [32]byte
	key, err := DeriveGenerationTwoKey(masterKey, []byte(info))
	if err != nil {
		return out, err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	for _, field := range fields {
		_, _ = mac.Write(generationTwoField(field))
	}
	copy(out[:], mac.Sum(nil))
	return out, nil
}

func generationTwoRawHMAC(masterKey []byte, info string, fields ...[]byte) ([32]byte, error) {
	var out [32]byte
	key, err := DeriveGenerationTwoKey(masterKey, []byte(info))
	if err != nil {
		return out, err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	for _, field := range fields {
		_, _ = mac.Write(field)
	}
	copy(out[:], mac.Sum(nil))
	return out, nil
}

// ComputeCredentialReportFingerprint derives the cross-case credential
// fingerprint. It intentionally does not reuse request, target, or source-IP
// keys; each rail has its own HKDF info label.
func ComputeCredentialReportFingerprint(masterKey []byte, connector, canonicalBaseURL, credential string) ([32]byte, error) {
	if !validUTF8Fields(connector, canonicalBaseURL, credential) {
		return [32]byte{}, ErrInvalidWideScalar
	}
	return generationTwoHMAC(masterKey, credentialReportFingerprintInfo, []byte(connector), []byte(canonicalBaseURL), []byte(credential))
}

func ComputeCredentialReportRequestHash(masterKey []byte, connector, canonicalBaseURL, credential, note string) ([32]byte, error) {
	if !validUTF8Fields(connector, canonicalBaseURL, credential, note) {
		return [32]byte{}, ErrInvalidWideScalar
	}
	return generationTwoHMAC(masterKey, credentialReportIdempotencyInfo, []byte(connector), []byte(canonicalBaseURL), []byte(credential), []byte(note))
}

func ComputeCredentialReportMaterialHash(masterKey []byte, caseID string, requestHash [32]byte) ([32]byte, error) {
	if !validCredentialReportCaseID(caseID) {
		return [32]byte{}, ErrInvalidWideScalar
	}
	return generationTwoRawHMAC(masterKey, credentialReportIdempotencyInfo, generationTwoField([]byte(caseID)), requestHash[:])
}

func ComputeCredentialReportTargetRef(masterKey []byte, caseID string, endpointKeyID int64) ([32]byte, error) {
	if !validCredentialReportCaseID(caseID) || endpointKeyID <= 0 {
		return [32]byte{}, ErrInvalidWideScalar
	}
	return generationTwoHMAC(masterKey, credentialReportTargetInfo, []byte(caseID), []byte(strconv.FormatInt(endpointKeyID, 10)))
}

func ComputeCredentialReportRateToken(masterKey []byte, scope string, scopeValue []byte) ([32]byte, error) {
	if !validCredentialReportRateScope(scope, scopeValue) {
		return [32]byte{}, ErrInvalidWideScalar
	}
	return generationTwoRawHMAC(masterKey, credentialReportRateInfo, generationTwoField([]byte(scope)), generationTwoField(scopeValue))
}

func validCredentialReportRateScope(scope string, scopeValue []byte) bool {
	switch scope {
	case "ip":
		return len(scopeValue) == 16
	case "account":
		// Account bucket values are canonical positive decimal ASCII, not an
		// unchecked integer conversion. This keeps equivalent spellings from
		// creating separate buckets and permits the full frozen decimal range.
		if len(scopeValue) == 0 || !validUnsignedDecimal(string(scopeValue)) || string(scopeValue) == "0" {
			return false
		}
		for _, b := range scopeValue {
			if b > 0x7f {
				return false
			}
		}
		return true
	case "fingerprint":
		return len(scopeValue) == sha256.Size
	case "global":
		return len(scopeValue) == 0
	default:
		return false
	}
}

func validCredentialReportCaseID(caseID string) bool {
	return ValidateOpaqueID(caseID, "rpc_")
}

func validUTF8Fields(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

// EncryptCredentialReportSourceIP seals the trusted-proxy canonical 16-byte
// address in the frozen report-material envelope. The case and material hash
// are authenticated as AAD, so an envelope cannot be moved between report
// materials. The returned bytes are exactly
// 0x01 || nonce[12] || ciphertext[16] || tag[16].
func EncryptCredentialReportSourceIP(masterKey []byte, caseID string, materialHash [32]byte, canonicalIP []byte) ([]byte, error) {
	if !validCredentialReportCaseID(caseID) || len(canonicalIP) != 16 {
		return nil, ErrInvalidSourceIPEnvelope
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrInvalidSourceIPEnvelope
	}
	return encryptCredentialReportSourceIPWithNonce(masterKey, caseID, materialHash, canonicalIP, nonce)
}

// EncryptCredentialReportSourceIPEnvelope is an explicit envelope-named alias
// for callers handling the report_materials source_ip_envelope column.
func EncryptCredentialReportSourceIPEnvelope(masterKey []byte, caseID string, materialHash [32]byte, canonicalIP []byte) ([]byte, error) {
	return EncryptCredentialReportSourceIP(masterKey, caseID, materialHash, canonicalIP)
}

// encryptCredentialReportSourceIPWithNonce is kept private so production
// callers always receive a CSPRNG nonce. Tests use it for a fixed AES-GCM
// vector without weakening the public API's nonce rule.
func encryptCredentialReportSourceIPWithNonce(masterKey []byte, caseID string, materialHash [32]byte, canonicalIP, nonce []byte) ([]byte, error) {
	if !validCredentialReportCaseID(caseID) || len(canonicalIP) != 16 || len(nonce) != 12 {
		return nil, ErrInvalidSourceIPEnvelope
	}
	key, err := DeriveGenerationTwoKey(masterKey, []byte(credentialReportSourceIPInfo))
	if err != nil {
		return nil, ErrInvalidSourceIPEnvelope
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidSourceIPEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != len(nonce) || aead.Overhead() != 16 {
		return nil, ErrInvalidSourceIPEnvelope
	}
	sealed := aead.Seal(nil, nonce, canonicalIP, credentialReportSourceIPAAD(caseID, materialHash))
	if len(sealed) != 16+16 {
		return nil, ErrInvalidSourceIPEnvelope
	}
	envelope := make([]byte, credentialReportSourceIPEnvelopeSize)
	envelope[0] = credentialReportSourceIPEnvelopeVersion
	copy(envelope[1:13], nonce)
	copy(envelope[13:], sealed)
	return envelope, nil
}

// DecryptCredentialReportSourceIP opens a report-material source IP after
// authenticating its version, key, case AAD, material AAD, and GCM tag. All
// malformed and authentication failures intentionally return one error so
// callers cannot turn this helper into an oracle.
func DecryptCredentialReportSourceIP(masterKey []byte, caseID string, materialHash [32]byte, envelope []byte) ([]byte, error) {
	if !validCredentialReportCaseID(caseID) || len(envelope) != credentialReportSourceIPEnvelopeSize || envelope[0] != credentialReportSourceIPEnvelopeVersion {
		return nil, ErrInvalidSourceIPEnvelope
	}
	key, err := DeriveGenerationTwoKey(masterKey, []byte(credentialReportSourceIPInfo))
	if err != nil {
		return nil, ErrInvalidSourceIPEnvelope
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidSourceIPEnvelope
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != 12 || aead.Overhead() != 16 {
		return nil, ErrInvalidSourceIPEnvelope
	}
	plaintext, err := aead.Open(nil, envelope[1:13], envelope[13:], credentialReportSourceIPAAD(caseID, materialHash))
	if err != nil || len(plaintext) != 16 {
		return nil, ErrInvalidSourceIPEnvelope
	}
	return plaintext, nil
}

// DecryptCredentialReportSourceIPEnvelope is the explicit envelope-named
// counterpart to EncryptCredentialReportSourceIPEnvelope.
func DecryptCredentialReportSourceIPEnvelope(masterKey []byte, caseID string, materialHash [32]byte, envelope []byte) ([]byte, error) {
	return DecryptCredentialReportSourceIP(masterKey, caseID, materialHash, envelope)
}

func credentialReportSourceIPAAD(caseID string, materialHash [32]byte) []byte {
	aad := generationTwoField([]byte(caseID))
	return append(aad, generationTwoField(materialHash[:])...)
}

// ComputeGameLeaderboardTieKey is stable across aggregate rebuilds while
// remaining isolated from report and rate digests. board is the canonical
// board name (for example "single" or "total"); mode is empty for Fishing
// and one of the canonical RPS modes for the RPS boards.
func ComputeGameLeaderboardTieKey(masterKey []byte, game, board, mode string, userID int64) ([32]byte, error) {
	if !validGameLeaderboardTuple(game, board, mode) || userID <= 0 {
		return [32]byte{}, ErrInvalidWideScalar
	}
	leaderboardKey, err := DeriveGenerationTwoKey(masterKey, []byte(gameLeaderboardTieInfo))
	if err != nil {
		return [32]byte{}, err
	}
	defer clear(leaderboardKey)
	return ComputeGameLeaderboardTieKeyFromDerivedKey(leaderboardKey, game, board, mode, userID)
}

// ComputeGameLeaderboardTieKeyFromDerivedKey computes the frozen leaderboard
// tie digest from Klb, the exact 32-byte key already derived with
// game-leaderboard-tie/v1. This lets opaque key owners expose only the
// purpose-bound key without making callers retain or reveal the process
// master key.
func ComputeGameLeaderboardTieKeyFromDerivedKey(leaderboardKey []byte, game, board, mode string, userID int64) ([32]byte, error) {
	if len(leaderboardKey) != sha256.Size || !validGameLeaderboardTuple(game, board, mode) || userID <= 0 {
		return [32]byte{}, ErrInvalidWideScalar
	}
	mac := hmac.New(sha256.New, leaderboardKey)
	for _, field := range [][]byte{[]byte(game), []byte(board), []byte(mode), []byte(strconv.FormatInt(userID, 10))} {
		_, _ = mac.Write(generationTwoField(field))
	}
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out, nil
}

func validGameLeaderboardTuple(game, board, mode string) bool {
	switch game {
	case "fishing":
		return (board == "single" || board == "total") && mode == ""
	case "rps":
		if board != "profit_rate" && board != "net_profit" {
			return false
		}
		switch mode {
		case "quick", "standard", "deathmatch":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

type CursorAtom struct {
	Kind   byte
	Uint   uint64
	Text   string
	Bytes  []byte
	U128   U128
	Signed SM128
}

const (
	CursorNull  byte = 0
	CursorUint  byte = 1
	CursorText  byte = 2
	CursorBytes byte = 3
	CursorU128  byte = 4
	CursorSM128 byte = 5
)

type PaginationCursor struct {
	Scope  string
	Owner  string
	Expiry uint64
	Atoms  []CursorAtom
}

func EncodePaginationCursor(masterKey []byte, scope, owner string, expiry uint64, atoms []CursorAtom) (string, error) {
	key, err := DeriveGenerationTwoKey(masterKey, []byte("pagination-cursor/v1"))
	if err != nil {
		return "", ErrInvalidCursor
	}
	defer clear(key)
	return EncodePaginationCursorWithDerivedKey(key, scope, owner, expiry, atoms)
}

// EncodePaginationCursorWithDerivedKey accepts only the canonical Kc already
// derived for pagination-cursor/v1. It lets Vault-backed callers avoid a
// second HKDF while preserving the exact frozen cursor bytes.
func EncodePaginationCursorWithDerivedKey(key []byte, scope, owner string, expiry uint64, atoms []CursorAtom) (string, error) {
	if len(key) != sha256.Size {
		return "", ErrInvalidCursor
	}
	if scope == "" || !utf8.ValidString(scope) || len(scope) > maxCursorBytes || !utf8.ValidString(owner) || len(owner) > maxCursorBytes || expiry > maxCursorExpiry || len(atoms) > 65535 {
		return "", ErrInvalidCursor
	}
	payload := make([]byte, 0, 64+len(atoms)*20)
	payload = append(payload, cursorVersion)
	payload = appendCursorField(payload, []byte(scope))
	payload = appendCursorField(payload, []byte(owner))
	var u64 [8]byte
	binary.BigEndian.PutUint64(u64[:], expiry)
	payload = append(payload, u64[:]...)
	var u16 [2]byte
	binary.BigEndian.PutUint16(u16[:], uint16(len(atoms)))
	payload = append(payload, u16[:]...)
	for _, atom := range atoms {
		encoded, ok := encodeCursorAtom(atom)
		if !ok {
			return "", ErrInvalidCursor
		}
		payload = append(payload, encoded...)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	token := base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
	if len(token) > maxCursorBytes {
		return "", ErrInvalidCursor
	}
	return token, nil
}

func DecodePaginationCursor(masterKey []byte, token, expectedScope, expectedOwner string, now uint64) (PaginationCursor, error) {
	key, err := DeriveGenerationTwoKey(masterKey, []byte("pagination-cursor/v1"))
	if err != nil {
		return PaginationCursor{}, ErrInvalidCursor
	}
	defer clear(key)
	return DecodePaginationCursorWithDerivedKey(key, token, expectedScope, expectedOwner, now)
}

// DecodePaginationCursorWithDerivedKey is the Vault-backed counterpart of
// EncodePaginationCursorWithDerivedKey.
func DecodePaginationCursorWithDerivedKey(key []byte, token, expectedScope, expectedOwner string, now uint64) (PaginationCursor, error) {
	var out PaginationCursor
	if len(key) != sha256.Size {
		return out, ErrInvalidCursor
	}
	if token == "" || len(token) > maxCursorBytes || expectedScope == "" || len(expectedScope) > maxCursorBytes || len(expectedOwner) > maxCursorBytes || !utf8.ValidString(expectedScope) || !utf8.ValidString(expectedOwner) {
		return out, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+4+4+8+2+sha256.Size {
		return out, ErrInvalidCursor
	}
	payload, providedMAC := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	validMAC := hmac.Equal(providedMAC, mac.Sum(nil))
	if !validMAC || payload[0] != cursorVersion {
		return out, ErrInvalidCursor
	}
	pos := 1
	readField := func() ([]byte, bool) {
		if len(payload)-pos < 4 {
			return nil, false
		}
		n := int(binary.BigEndian.Uint32(payload[pos : pos+4]))
		pos += 4
		if n < 0 || n > maxCursorBytes || n > len(payload)-pos {
			return nil, false
		}
		field := payload[pos : pos+n]
		pos += n
		return field, true
	}
	scope, ok := readField()
	if !ok || len(scope) == 0 || !utf8.Valid(scope) {
		return out, ErrInvalidCursor
	}
	owner, ok := readField()
	if !ok || !utf8.Valid(owner) {
		return out, ErrInvalidCursor
	}
	if len(payload)-pos < 10 {
		return out, ErrInvalidCursor
	}
	out.Scope, out.Owner = string(scope), string(owner)
	out.Expiry = binary.BigEndian.Uint64(payload[pos : pos+8])
	pos += 8
	count := int(binary.BigEndian.Uint16(payload[pos : pos+2]))
	pos += 2
	if out.Scope != expectedScope || out.Owner != expectedOwner || out.Expiry > maxCursorExpiry || out.Expiry <= now || count > len(payload)-pos {
		return PaginationCursor{}, ErrInvalidCursor
	}
	out.Atoms = make([]CursorAtom, 0, count)
	for i := 0; i < count; i++ {
		atom, next, ok := decodeCursorAtom(payload, pos)
		if !ok {
			return PaginationCursor{}, ErrInvalidCursor
		}
		pos = next
		out.Atoms = append(out.Atoms, atom)
	}
	if pos != len(payload) {
		return PaginationCursor{}, ErrInvalidCursor
	}
	canonical, err := EncodePaginationCursorWithDerivedKey(key, out.Scope, out.Owner, out.Expiry, out.Atoms)
	if err != nil || canonical != token {
		return PaginationCursor{}, ErrInvalidCursor
	}
	return out, nil
}

func appendCursorField(dst, value []byte) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	dst = append(dst, size[:]...)
	return append(dst, value...)
}

func encodeCursorAtom(atom CursorAtom) ([]byte, bool) {
	switch atom.Kind {
	case CursorNull:
		return []byte{CursorNull}, true
	case CursorUint:
		var b [9]byte
		b[0] = CursorUint
		binary.BigEndian.PutUint64(b[1:], atom.Uint)
		return b[:], true
	case CursorText:
		if !utf8.ValidString(atom.Text) || len(atom.Text) > maxCursorBytes {
			return nil, false
		}
		return append([]byte{CursorText}, appendCursorField(nil, []byte(atom.Text))...), true
	case CursorBytes:
		if len(atom.Bytes) > maxCursorBytes {
			return nil, false
		}
		return append([]byte{CursorBytes}, appendCursorField(nil, atom.Bytes)...), true
	case CursorU128:
		return append([]byte{CursorU128}, atom.U128[:]...), true
	case CursorSM128:
		value, err := EncodeSM128(atom.Signed)
		return append([]byte{CursorSM128}, value...), err == nil
	default:
		return nil, false
	}
}

func decodeCursorAtom(payload []byte, pos int) (CursorAtom, int, bool) {
	var out CursorAtom
	if pos >= len(payload) {
		return out, pos, false
	}
	out.Kind = payload[pos]
	pos++
	switch out.Kind {
	case CursorNull:
		return out, pos, true
	case CursorUint:
		if len(payload)-pos < 8 {
			return out, pos, false
		}
		out.Uint = binary.BigEndian.Uint64(payload[pos : pos+8])
		return out, pos + 8, true
	case CursorText, CursorBytes:
		if len(payload)-pos < 4 {
			return out, pos, false
		}
		n := int(binary.BigEndian.Uint32(payload[pos : pos+4]))
		pos += 4
		if n < 0 || n > len(payload)-pos {
			return out, pos, false
		}
		value := append([]byte(nil), payload[pos:pos+n]...)
		if out.Kind == CursorText {
			if !utf8.Valid(value) {
				return out, pos, false
			}
			out.Text = string(value)
		} else {
			out.Bytes = value
		}
		return out, pos + n, true
	case CursorU128:
		if len(payload)-pos < 16 {
			return out, pos, false
		}
		copy(out.U128[:], payload[pos:pos+16])
		return out, pos + 16, true
	case CursorSM128:
		if len(payload)-pos < 17 {
			return out, pos, false
		}
		value, err := DecodeSM128(payload[pos : pos+17])
		if err != nil {
			return out, pos, false
		}
		out.Signed = value
		return out, pos + 17, true
	default:
		return out, pos, false
	}
}

// DecimalID is used by repositories when projecting SQLite integer IDs into
// a JSON-safe string field.
func DecimalID(id int64) (string, error) {
	if id <= 0 {
		return "", ErrInvalidWideScalar
	}
	return strconv.FormatInt(id, 10), nil
}
