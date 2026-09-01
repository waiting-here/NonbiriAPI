package db

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func mustBig(t *testing.T, text string) *big.Int {
	t.Helper()
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		t.Fatalf("parse big integer %q", text)
	}
	return value
}

func mustHash32(t *testing.T, text string) [32]byte {
	t.Helper()
	var out [32]byte
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != len(out) {
		t.Fatalf("decode hash %q: %v", text, err)
	}
	copy(out[:], raw)
	return out
}

func expectWideError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected wide-scalar validation error")
	}
}

func TestWideScalarsCanonicalAndHostile(t *testing.T) {
	const max128 = "340282366920938463463374607431768211455"
	const max127 = "170141183460469231731687303715884105727"
	const max256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

	for _, text := range []string{"0", "1", max128} {
		value, err := ParseU128Decimal(text)
		if err != nil || value.Decimal() != text {
			t.Fatalf("U128 canonical %q -> %s, %v", text, value.Decimal(), err)
		}
		encoded := EncodeU128(value)
		if len(encoded) != 16 {
			t.Fatalf("U128 encoded length = %d", len(encoded))
		}
		decoded, err := DecodeU128(encoded)
		if err != nil || decoded != value {
			t.Fatalf("U128 round trip = %v, %v", decoded, err)
		}
	}
	for _, text := range []string{"0", "1", max256} {
		value, err := ParseU256Decimal(text)
		if err != nil || value.Decimal() != text {
			t.Fatalf("U256 canonical %q -> %s, %v", text, value.Decimal(), err)
		}
		encoded := EncodeU256(value)
		if len(encoded) != 32 {
			t.Fatalf("U256 encoded length = %d", len(encoded))
		}
		decoded, err := DecodeU256(encoded)
		if err != nil || decoded != value {
			t.Fatalf("U256 round trip = %v, %v", decoded, err)
		}
	}

	for _, text := range []string{"", "+1", "01", "00", " 1", "1 ", "1e3", "-1", "0x1", "1.0"} {
		expectWideError(t, func() error { _, err := ParseU128Decimal(text); return err }())
		expectWideError(t, func() error { _, err := ParseU256Decimal(text); return err }())
	}
	if _, err := ParseU128Decimal(max128 + "0"); err == nil {
		t.Fatal("U128 overflow accepted")
	}
	if _, err := ParseU256Decimal(max256 + "0"); err == nil {
		t.Fatal("U256 overflow accepted")
	}
	if _, err := DecodeU128(make([]byte, 15)); err == nil {
		t.Fatal("short U128 accepted")
	}
	if _, err := DecodeU256(make([]byte, 33)); err == nil {
		t.Fatal("long U256 accepted")
	}
	if _, err := U128FromBig(mustBig(t, "-1")); err == nil {
		t.Fatal("negative U128 accepted")
	}
	if _, err := U256FromBig(nil); err == nil {
		t.Fatal("nil U256 accepted")
	}

	for _, text := range []string{"-" + max127, "-1", "0", "1", max127} {
		value, err := ParseSM128Decimal(text)
		if err != nil || value.Decimal() != text {
			t.Fatalf("SM128 canonical %q -> %s, %v", text, value.Decimal(), err)
		}
		encoded, err := EncodeSM128(value)
		if err != nil || len(encoded) != 17 {
			t.Fatalf("SM128 encode %q: len=%d err=%v", text, len(encoded), err)
		}
		decoded, err := DecodeSM128(encoded)
		if err != nil || decoded != value {
			t.Fatalf("SM128 round trip %q -> %v, %v", text, decoded, err)
		}
		wantSignCode := byte(1)
		if strings.HasPrefix(text, "-") {
			wantSignCode = 0
		} else if text != "0" {
			wantSignCode = 2
		}
		if encoded[0] != wantSignCode {
			t.Fatalf("SM128 sign code %q = %#x, want %#x", text, encoded[0], wantSignCode)
		}
	}
	for _, text := range []string{"-0", "+0", "+1", "01", "-01", " 1", "1 ", "1e3", "1.0", "-1.0"} {
		if _, err := ParseSM128Decimal(text); err == nil {
			t.Fatalf("hostile SM128 %q accepted", text)
		}
	}
	for _, text := range []string{"170141183460469231731687303715884105728", "-170141183460469231731687303715884105728"} {
		if _, err := ParseSM128Decimal(text); err == nil {
			t.Fatalf("SM128 boundary overflow %q accepted", text)
		}
	}
	if _, err := NewSM128(0, make([]byte, 16)); err != nil {
		t.Fatalf("canonical SM128 zero rejected: %v", err)
	}
	if _, err := NewSM128(-1, make([]byte, 16)); err == nil {
		t.Fatal("negative zero accepted")
	}
	if _, err := NewSM128(1, []byte{0x80}); err == nil {
		t.Fatal("short high-bit SM128 accepted")
	}
	badMagnitude := make([]byte, 16)
	badMagnitude[0] = 0x80
	if _, err := NewSM128(1, badMagnitude); err == nil {
		t.Fatal("high-bit SM128 magnitude accepted")
	}
	if _, err := DecodeSM128(append([]byte{0}, make([]byte, 16)...)); err == nil {
		t.Fatal("zero sign with nonzero sign-code encoding accepted")
	}
	if _, err := DecodeSM128(append([]byte{2}, make([]byte, 16)...)); err == nil {
		t.Fatal("positive sign with zero magnitude accepted")
	}
	if _, err := DecodeSM128(append([]byte{3}, make([]byte, 16)...)); err == nil {
		t.Fatal("unknown SM128 sign-code accepted")
	}

	input := bytes.Repeat([]byte{0x5a}, 16)
	decoded, err := DecodeU128(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 0
	if decoded[0] != 0x5a {
		t.Fatal("U128 decoder retained caller buffer")
	}
}

func TestOpaqueIDCanonicalAndHostile(t *testing.T) {
	prefixes := []string{
		"ann_", "op_", "req_", "clm_", "pol_", "thu_", "fb_", "ll_", "rpsq_", "rps_", "rpc_", "rpt_", "iss_", "lgh_", "b1e_", "sse_", "thp_", "gle_", "dbs_", "dbt_", "dbe_",
	}
	raw := make([]byte, 16)
	body := base64.RawURLEncoding.EncodeToString(raw)
	if len(body) != maxOpaqueIDBody || body[len(body)-1] != 'A' {
		t.Fatalf("zero OID body = %q", body)
	}
	for _, prefix := range prefixes {
		value := prefix + body
		if !ValidateOpaqueID(value, prefix) {
			t.Errorf("canonical OID rejected: %q", value)
		}
		generated, err := GenerateOpaqueID(prefix)
		if err != nil || !ValidateOpaqueID(generated, prefix) {
			t.Errorf("generated %q: %v", generated, err)
		}
	}
	if ValidateOpaqueID("unknown_"+body, "unknown_") || ValidateOpaqueID("abc", "ann_") {
		t.Fatal("unknown/short OID accepted")
	}
	for _, value := range []string{
		"ann_" + body + "A",              // 23 body chars
		"ann_" + body[:21],               // 21 body chars
		"ann_" + body[:21] + "B",         // non-zero trailing base64 bits
		"ann_" + body[:21] + "=",         // padded encoding
		"ann_" + strings.Repeat("!", 22), // invalid alphabet
		"ann_" + body[:21] + "\x00",      // control byte
	} {
		if ValidateOpaqueID(value, "ann_") {
			t.Fatalf("hostile OID accepted: %q", value)
		}
	}
	if _, err := GenerateOpaqueID("ann"); err == nil {
		t.Fatal("malformed prefix accepted")
	}
}

func TestHKDFRFC5869Vector(t *testing.T) {
	ikm := bytes.Repeat([]byte{0x0b}, 22)
	salt, _ := hex.DecodeString("000102030405060708090a0b0c")
	info, _ := hex.DecodeString("f0f1f2f3f4f5f6f7f8f9")
	want, _ := hex.DecodeString("3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")
	got, err := HKDFSHA256(ikm, salt, info, len(want))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("HKDF RFC5869 = %x, %v; want %x", got, err, want)
	}
}

func TestGenerationTwoVaultSubkeyMatchesFixedSaltGolden(t *testing.T) {
	master, err := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	de, err := DeriveGenerationTwoKey(master, []byte("pagination-cursor/v1"))
	if err != nil {
		t.Fatalf("derive codec key: %v", err)
	}
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	deferredClose := vault
	t.Cleanup(func() { _ = deferredClose.Close() })
	secretKey, err := vault.DeriveGenerationTwoSubkey([]byte("pagination-cursor/v1"))
	if err != nil {
		t.Fatalf("derive vault subkey: %v", err)
	}
	defer clear(secretKey)
	want := "7b2a6066eba7f7f18b0fb48c153cb895afd08d172dbb585b51d096cc62c06331"
	if got := hex.EncodeToString(de); got != want {
		t.Fatalf("codec fixed-salt golden=%s, want %s", got, want)
	}
	if got := hex.EncodeToString(secretKey); got != want {
		t.Fatalf("vault fixed-salt golden=%s, want %s", got, want)
	}
}

func TestGenerationTwoDigestVectors(t *testing.T) {
	master, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	fingerprint, err := ComputeCredentialReportFingerprint(master, "openai", "https://api.example.test/v1", "sk-secret")
	if err != nil {
		t.Fatal(err)
	}
	requestHash, err := ComputeCredentialReportRequestHash(master, "openai", "https://api.example.test/v1", "sk-secret", "备注")
	if err != nil {
		t.Fatal(err)
	}
	emptyNoteRequestHash, err := ComputeCredentialReportRequestHash(master, "openai", "https://api.example.test/v1", "sk-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	materialHash, err := ComputeCredentialReportMaterialHash(master, "rpc_000000000000000000000A", requestHash)
	if err != nil {
		t.Fatal(err)
	}
	targetRef, err := ComputeCredentialReportTargetRef(master, "rpc_000000000000000000000A", 42)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintRate, err := ComputeCredentialReportRateToken(master, "fingerprint", fingerprint[:])
	if err != nil {
		t.Fatal(err)
	}
	accountRate, err := ComputeCredentialReportRateToken(master, "account", []byte("42"))
	if err != nil {
		t.Fatal(err)
	}
	ipRate, err := ComputeCredentialReportRateToken(master, "ip", net.ParseIP("192.0.2.1").To16())
	if err != nil {
		t.Fatal(err)
	}
	globalRate, err := ComputeCredentialReportRateToken(master, "global", nil)
	if err != nil {
		t.Fatal(err)
	}
	tie, err := ComputeGameLeaderboardTieKey(master, "rps", "net_profit", "deathmatch", 42)
	if err != nil {
		t.Fatal(err)
	}
	leaderboardKey, err := DeriveGenerationTwoKey(master, []byte(gameLeaderboardTieInfo))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(leaderboardKey)
	derivedTie, err := ComputeGameLeaderboardTieKeyFromDerivedKey(leaderboardKey, "rps", "net_profit", "deathmatch", 42)
	if err != nil {
		t.Fatal(err)
	}
	if derivedTie != tie {
		t.Fatalf("derived leaderboard tie=%x, want %x", derivedTie, tie)
	}
	// Values are fixed golden vectors for the frozen field framing and HKDF
	// info labels. Keep each expected value independent so a domain mix-up is
	// visible even when the underlying HMAC primitive remains correct.
	wants := map[string]string{
		"fingerprint":             "85210b4bbf3a3fe0e8bf0f3c5cac52a324ee6bc01d4bf552a393ca0abaaec3bb",
		"request_hash":            "2a872296409d67fe8d559f31fc54d14c37134fdaead5080639c03908c17d9751",
		"request_hash_empty_note": "92eaf6cd78df5f27e9c51903c07e3424a252e03ed7c81a9b0150808b3440a75f",
		"material_hash":           "4c20a7ae19173ed748ccd1d0555427916e7c0fa6ed574eeb2289cdecfe8b2492",
		"target_ref":              "cd69802e5db77df237e1a9fa5f1513f076d54266fb8eb081d09b533b5784c804",
		"fingerprint_rate":        "9aaee10371bf50a8802c7b9351615ca9689443609ca260567b6a4a9026f00776",
		"account_rate":            "d0772a668cbbe122e28b82a488c7ed2bb2b3096252de04a602f06faf9a3ef2bb",
		"ip_rate":                 "5f0cecc7e8a186e0973d3e3194079a9e88eb396b3fbbd1a5824fd55e05557541",
		"global_rate":             "919b29609f66f4102445493f119510e000edf103fcd53b49699b449b921f51b6",
		"leaderboard_tie":         "6f6f37c0f8d533fc286639fd8dd6aea71e4875498d406d0b83aed5e38c37beeb",
	}
	values := map[string][32]byte{
		"fingerprint":             fingerprint,
		"request_hash":            requestHash,
		"request_hash_empty_note": emptyNoteRequestHash,
		"material_hash":           materialHash,
		"target_ref":              targetRef,
		"fingerprint_rate":        fingerprintRate,
		"account_rate":            accountRate,
		"ip_rate":                 ipRate,
		"global_rate":             globalRate,
		"leaderboard_tie":         tie,
	}
	for name, value := range values {
		got := hex.EncodeToString(value[:])
		if got != wants[name] {
			t.Errorf("%s=%s, want %s", name, got, wants[name])
		}
	}
	if fingerprint == requestHash || requestHash == targetRef || fingerprintRate == accountRate || accountRate == globalRate {
		t.Fatal("digest domains unexpectedly collided")
	}
	for _, key := range [][]byte{nil, make([]byte, 31), make([]byte, 33)} {
		if _, err := ComputeGameLeaderboardTieKeyFromDerivedKey(key, "rps", "net_profit", "deathmatch", 42); err == nil {
			t.Fatalf("derived leaderboard key length %d accepted", len(key))
		}
	}
	if _, err := ComputeCredentialReportRateToken(master, "account", []byte("0")); err == nil {
		t.Fatal("zero account rate scope accepted")
	}
	for _, value := range [][]byte{[]byte("+42"), []byte("042"), []byte("42 "), []byte("4e2")} {
		if _, err := ComputeCredentialReportRateToken(master, "account", value); err == nil {
			t.Fatalf("hostile account rate scope %q accepted", value)
		}
	}
	for _, scope := range []string{"nope", "IP", ""} {
		if _, err := ComputeCredentialReportRateToken(master, scope, nil); err == nil {
			t.Fatalf("hostile rate scope %q accepted", scope)
		}
	}
	if _, err := ComputeCredentialReportRateToken(master, "global", []byte{1}); err == nil {
		t.Fatal("nonempty global rate scope accepted")
	}
	if _, err := ComputeCredentialReportMaterialHash(master, "not-a-report-case", requestHash); err == nil {
		t.Fatal("non-canonical report case accepted for material hash")
	}
	if _, err := ComputeCredentialReportTargetRef(master, "rpc_short", 42); err == nil {
		t.Fatal("non-canonical report case accepted for target ref")
	}
	if _, err := ComputeCredentialReportFingerprint(master, "\xff", "https://api.example.test/v1", "secret"); err == nil {
		t.Fatal("invalid UTF-8 credential fingerprint input accepted")
	}
}

func TestCredentialReportDigestFieldFramingEmptyAndUnicode(t *testing.T) {
	master, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	const endpoint = "https://api.example.test/v1"
	const secret = "sk-secret"

	base, err := ComputeCredentialReportRequestHash(master, "openai", endpoint, secret, "")
	if err != nil {
		t.Fatal(err)
	}
	// F(s) always includes a four-byte byte length, including for an empty
	// field. Distinct endpoint/secret/note inputs therefore cannot collapse into
	// the same framed request hash.
	variants := []struct {
		name      string
		connector string
		endpoint  string
		secret    string
		note      string
	}{
		{name: "empty connector", connector: "", endpoint: endpoint, secret: secret},
		{name: "empty endpoint", connector: "openai", endpoint: "", secret: secret},
		{name: "empty secret", connector: "openai", endpoint: endpoint, secret: ""},
		{name: "unicode endpoint", connector: "openai", endpoint: "https://例子.test/v1", secret: secret},
		{name: "unicode secret", connector: "openai", endpoint: endpoint, secret: "密钥"},
		{name: "unicode note", connector: "openai", endpoint: endpoint, secret: secret, note: "备注"},
	}
	for _, variant := range variants {
		got, err := ComputeCredentialReportRequestHash(master, variant.connector, variant.endpoint, variant.secret, variant.note)
		if err != nil {
			t.Fatalf("%s: %v", variant.name, err)
		}
		if got == base {
			t.Fatalf("%s collapsed into the empty-note base hash", variant.name)
		}
	}

	// The route validator owns the frozen endpoint (4,096 bytes), secret
	// (64 KiB), and note (2,048 runes) caps. This cryptographic primitive only
	// validates UTF-8 and preserves accepted values exactly; it must not add a
	// second, inconsistent route-size policy. Empty and Unicode values above
	// exercise the framing boundary without changing that ownership.
	for _, tc := range []struct {
		name   string
		invoke func() error
	}{
		{name: "invalid connector", invoke: func() error {
			_, err := ComputeCredentialReportRequestHash(master, string([]byte{0xff}), endpoint, secret, "")
			return err
		}},
		{name: "invalid endpoint", invoke: func() error {
			_, err := ComputeCredentialReportRequestHash(master, "openai", string([]byte{0xff}), secret, "")
			return err
		}},
		{name: "invalid secret", invoke: func() error {
			_, err := ComputeCredentialReportRequestHash(master, "openai", endpoint, string([]byte{0xff}), "")
			return err
		}},
		{name: "invalid note", invoke: func() error {
			_, err := ComputeCredentialReportRequestHash(master, "openai", endpoint, secret, string([]byte{0xff}))
			return err
		}},
	} {
		if tc.invoke() == nil {
			t.Fatalf("%s accepted invalid UTF-8", tc.name)
		}
	}
}

func TestCredentialReportSourceIPEnvelope(t *testing.T) {
	master, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	caseID := "rpc_000000000000000000000A"
	var material [32]byte
	for i := range material {
		material[i] = byte(0xa0 + i)
	}
	ip := net.ParseIP("192.0.2.1").To16()
	nonce, _ := hex.DecodeString("000102030405060708090a0b")
	envelope, err := encryptCredentialReportSourceIPWithNonce(master, caseID, material, ip, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope) != CredentialReportSourceIPEnvelopeSize || envelope[0] != CredentialReportSourceIPEnvelopeVersion {
		t.Fatalf("envelope shape = %d/%x", len(envelope), envelope)
	}
	// Fixed vector is filled below from the independently specified AES-GCM
	// construction (key=HKDF source-ip info, AAD=F(case)||FB(material)).
	const wantEnvelope = "01000102030405060708090a0b51f23aaeb18b576bcf828c237d4b8223547290256454ce71d9b0436ee4337a09"
	if hex.EncodeToString(envelope) != wantEnvelope {
		t.Fatalf("source IP envelope = %x, want %s", envelope, wantEnvelope)
	}
	opened, err := DecryptCredentialReportSourceIP(master, caseID, material, envelope)
	if err != nil || !bytes.Equal(opened, ip) {
		t.Fatalf("decrypt = %x, %v", opened, err)
	}
	otherCase := caseID + "x"
	if _, err := DecryptCredentialReportSourceIP(master, otherCase, material, envelope); err != ErrInvalidSourceIPEnvelope {
		t.Fatalf("cross-case decrypt error = %v", err)
	}
	otherMaterial := material
	otherMaterial[0]++
	if _, err := DecryptCredentialReportSourceIP(master, caseID, otherMaterial, envelope); err != ErrInvalidSourceIPEnvelope {
		t.Fatalf("cross-material decrypt error = %v", err)
	}
	otherMaster := append([]byte(nil), master...)
	otherMaster[0]++
	if _, err := DecryptCredentialReportSourceIP(otherMaster, caseID, material, envelope); err != ErrInvalidSourceIPEnvelope {
		t.Fatalf("cross-key decrypt error = %v", err)
	}
	for _, index := range []int{0, 1, 12, 13, len(envelope) - 1} {
		mutated := append([]byte(nil), envelope...)
		mutated[index]++
		if _, err := DecryptCredentialReportSourceIP(master, caseID, material, mutated); err != ErrInvalidSourceIPEnvelope {
			t.Fatalf("tampered envelope index %d error = %v", index, err)
		}
	}
	for _, length := range []int{0, 1, CredentialReportSourceIPEnvelopeSize - 1, CredentialReportSourceIPEnvelopeSize + 1} {
		if _, err := DecryptCredentialReportSourceIP(master, caseID, material, make([]byte, length)); err != ErrInvalidSourceIPEnvelope {
			t.Fatalf("length %d error = %v", length, err)
		}
	}
	if _, err := EncryptCredentialReportSourceIP(master, caseID, material, ip[:15]); err != ErrInvalidSourceIPEnvelope {
		t.Fatal("short canonical IP accepted")
	}
	if _, err := EncryptCredentialReportSourceIP(master, "rpc_short", material, ip); err != ErrInvalidSourceIPEnvelope {
		t.Fatal("non-canonical report case accepted by source-IP envelope")
	}
	randomEnvelope, err := EncryptCredentialReportSourceIP(master, caseID, material, ip)
	if err != nil || len(randomEnvelope) != CredentialReportSourceIPEnvelopeSize {
		t.Fatalf("random envelope = %x, %v", randomEnvelope, err)
	}
	if _, err := DecryptCredentialReportSourceIPEnvelope(master, caseID, material, randomEnvelope); err != nil {
		t.Fatalf("envelope alias decrypt: %v", err)
	}
	ipv6 := net.ParseIP("2001:db8::1234").To16()
	nonceV6, _ := hex.DecodeString("0c0d0e0f1011121314151617")
	envelopeV6, err := encryptCredentialReportSourceIPWithNonce(master, caseID, material, ipv6, nonceV6)
	if err != nil {
		t.Fatal(err)
	}
	const wantEnvelopeV6 = "010c0d0e0f1011121314151617b102d6170d3af0fd0c8a63596bcfdfb33b8794a82c77a000e1cf900a2366cf89"
	if hex.EncodeToString(envelopeV6) != wantEnvelopeV6 {
		t.Fatalf("IPv6 source IP envelope = %x, want %s", envelopeV6, wantEnvelopeV6)
	}
	openedV6, err := DecryptCredentialReportSourceIP(master, caseID, material, envelopeV6)
	if err != nil || !bytes.Equal(openedV6, ipv6) {
		t.Fatalf("IPv6 decrypt = %x, %v", openedV6, err)
	}
}

func TestPaginationCursorCanonicalAndHostile(t *testing.T) {
	master, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	negative, err := ParseSM128Decimal("-1")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := ParseSM128Decimal("0")
	if err != nil {
		t.Fatal(err)
	}
	positive, err := ParseSM128Decimal("170141183460469231731687303715884105727")
	if err != nil {
		t.Fatal(err)
	}
	u128, err := ParseU128Decimal("340282366920938463463374607431768211455")
	if err != nil {
		t.Fatal(err)
	}
	atoms := []CursorAtom{
		{Kind: CursorNull},
		{Kind: CursorUint, Uint: ^uint64(0)},
		{Kind: CursorText, Text: "游标"},
		{Kind: CursorBytes, Bytes: []byte{0, 1, 0xff}},
		{Kind: CursorU128, U128: u128},
		{Kind: CursorSM128, Signed: negative},
		{Kind: CursorSM128, Signed: zero},
		{Kind: CursorSM128, Signed: positive},
	}
	token, err := EncodePaginationCursor(master, "user-endpoints", "user-42", maxCursorExpiry, atoms)
	if err != nil {
		t.Fatal(err)
	}
	const wantToken = "AQAAAA51c2VyLWVuZHBvaW50cwAAAAd1c2VyLTQyAAAAOv_0QX8ACAAB__________8CAAAABua4uOaghwMAAAADAAH_BP____________________8FAAAAAAAAAAAAAAAAAAAAAAEFAQAAAAAAAAAAAAAAAAAAAAAFAn_____________________9NyPZgYP1kttjQGy284B6jFoLqrs1Qu8VnvbURI8Qhw"
	if token != wantToken {
		t.Fatalf("cursor golden = %q, want %q", token, wantToken)
	}
	if strings.Contains(token, "=") || len(token) > maxCursorBytes {
		t.Fatalf("noncanonical cursor token %q", token)
	}
	decoded, err := DecodePaginationCursor(master, token, "user-endpoints", "user-42", maxCursorExpiry-1)
	if err != nil || decoded.Scope != "user-endpoints" || !reflect.DeepEqual(decoded.Atoms, atoms) {
		t.Fatalf("cursor round trip = %+v, %v", decoded, err)
	}
	for _, tc := range []struct {
		name  string
		scope string
		owner string
		now   uint64
		key   []byte
	}{
		{"scope", "other", "user-42", maxCursorExpiry - 1, master},
		{"owner", "user-endpoints", "other", maxCursorExpiry - 1, master},
		{"expired", "user-endpoints", "user-42", maxCursorExpiry, master},
		{"key", "user-endpoints", "user-42", maxCursorExpiry - 1, append([]byte(nil), master[:31]...)},
	} {
		if _, err := DecodePaginationCursor(tc.key, token, tc.scope, tc.owner, tc.now); err == nil {
			t.Fatalf("hostile cursor %s accepted", tc.name)
		}
	}
	if _, err := EncodePaginationCursor(master, "user-endpoints", "user-42", maxCursorExpiry+1, nil); err == nil {
		t.Fatal("cursor expiry over Tmax accepted")
	}
	if _, err := EncodePaginationCursor(master, "user-endpoints", "user-42", 1, []CursorAtom{{Kind: CursorText, Text: strings.Repeat("x", maxCursorBytes+1)}}); err == nil {
		t.Fatal("oversized cursor text accepted")
	}
	if _, err := EncodePaginationCursor(master, "user-endpoints", "user-42", 1, []CursorAtom{{Kind: CursorBytes, Bytes: bytes.Repeat([]byte{1}, maxCursorBytes+1)}}); err == nil {
		t.Fatal("oversized cursor bytes accepted")
	}

	// Empty expected owner is an exact value, never a wildcard.
	emptyOwnerToken, err := EncodePaginationCursor(master, "scope", "", maxCursorExpiry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePaginationCursor(master, emptyOwnerToken, "scope", "", maxCursorExpiry-1); err != nil {
		t.Fatalf("empty owner exact match rejected: %v", err)
	}
	nonemptyOwnerToken, err := EncodePaginationCursor(master, "scope", "owner", maxCursorExpiry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePaginationCursor(master, nonemptyOwnerToken, "scope", "", maxCursorExpiry-1); err == nil {
		t.Fatal("empty expected owner used as wildcard")
	}
	if _, err := DecodePaginationCursor(master, nonemptyOwnerToken, "", "owner", maxCursorExpiry-1); err == nil {
		t.Fatal("empty expected scope used as wildcard")
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-1] ^= 1
	if _, err := DecodePaginationCursor(master, base64.RawURLEncoding.EncodeToString(mutated), "user-endpoints", "user-42", maxCursorExpiry-1); err == nil {
		t.Fatal("cursor MAC tamper accepted")
	}
	mutated = append([]byte(nil), raw...)
	mutated[0] = 2
	payload, macKeyErr := DeriveGenerationTwoKey(master, []byte("pagination-cursor/v1"))
	if macKeyErr != nil {
		t.Fatal(macKeyErr)
	}
	mac := hmac.New(sha256.New, payload)
	_, _ = mac.Write(mutated[:len(mutated)-sha256.Size])
	copy(mutated[len(mutated)-sha256.Size:], mac.Sum(nil))
	clear(payload)
	if _, err := DecodePaginationCursor(master, base64.RawURLEncoding.EncodeToString(mutated), "user-endpoints", "user-42", maxCursorExpiry-1); err == nil {
		t.Fatal("cursor version downgrade accepted")
	}
	if _, err := DecodePaginationCursor(master, token+"=", "user-endpoints", "user-42", maxCursorExpiry-1); err == nil {
		t.Fatal("padded cursor accepted")
	}
}

func TestLeaderboardTieTupleWhitelist(t *testing.T) {
	master := bytes.Repeat([]byte{0x42}, 32)
	valid := [][3]string{
		{"fishing", "single", ""},
		{"fishing", "total", ""},
		{"rps", "profit_rate", "quick"},
		{"rps", "profit_rate", "standard"},
		{"rps", "profit_rate", "deathmatch"},
		{"rps", "net_profit", "quick"},
		{"rps", "net_profit", "standard"},
		{"rps", "net_profit", "deathmatch"},
	}
	seen := make(map[[32]byte]bool)
	for _, tuple := range valid {
		key, err := ComputeGameLeaderboardTieKey(master, tuple[0], tuple[1], tuple[2], 42)
		if err != nil {
			t.Fatalf("valid tuple %v: %v", tuple, err)
		}
		if seen[key] {
			t.Fatalf("tie key collision for tuple %v", tuple)
		}
		seen[key] = true
	}
	for _, tuple := range [][3]string{
		{"fishing", "single", "quick"},
		{"fishing", "profit_rate", ""},
		{"rps", "single", "quick"},
		{"rps", "net_profit", ""},
		{"rps", "net_profit", "all"},
		{"future", "single", ""},
	} {
		if _, err := ComputeGameLeaderboardTieKey(master, tuple[0], tuple[1], tuple[2], 42); err == nil {
			t.Fatalf("invalid tuple %v accepted", tuple)
		}
	}
	for _, id := range []int64{0, -1} {
		if _, err := ComputeGameLeaderboardTieKey(master, "fishing", "single", "", id); err == nil {
			t.Fatalf("invalid user id %d accepted", id)
		}
	}
}

func TestLeaderboardTieGoldenVectors(t *testing.T) {
	master, _ := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	cases := []struct {
		name  string
		game  string
		board string
		mode  string
		want  string
	}{
		{name: "fishing single", game: "fishing", board: "single", want: "292773c64dcdedef974f818721e03cbbbf81f618180ada9aba066d76722c40b6"},
		{name: "fishing total", game: "fishing", board: "total", want: "c3e652e854b26761165a2ab9baa7fa1b1e86c7161856d9b10fb8ba154df92fdb"},
		{name: "rps profit rate quick", game: "rps", board: "profit_rate", mode: "quick", want: "4c7c3446a77a78a3bbdf33ceb58ee7b6a5fe5076ea8ab93258346b335bdeb972"},
		{name: "rps profit rate standard", game: "rps", board: "profit_rate", mode: "standard", want: "8a1b5fbc5a02f8ea81996e179f58ff5735055e45affa4a79bd015ca25934db92"},
		{name: "rps profit rate deathmatch", game: "rps", board: "profit_rate", mode: "deathmatch", want: "878b15e88a85065fda396908247c12462bd53b4423f6cda942969445dd951ff7"},
		{name: "rps net profit quick", game: "rps", board: "net_profit", mode: "quick", want: "f7d7b42617b8b56ac58e8261f8a7f9313f6defcce60385da4c0a03b87e1d1422"},
		{name: "rps net profit standard", game: "rps", board: "net_profit", mode: "standard", want: "c0e77a6e1404ad41a8dc9f3456494f1ac16684ab10cc5737c2a06120c3c6af81"},
		{name: "rps net profit deathmatch", game: "rps", board: "net_profit", mode: "deathmatch", want: "6f6f37c0f8d533fc286639fd8dd6aea71e4875498d406d0b83aed5e38c37beeb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ComputeGameLeaderboardTieKey(master, tc.game, tc.board, tc.mode, 42)
			if err != nil {
				t.Fatal(err)
			}
			if encoded := hex.EncodeToString(got[:]); encoded != tc.want {
				t.Fatalf("tie key = %s, want %s", encoded, tc.want)
			}
		})
	}
}
