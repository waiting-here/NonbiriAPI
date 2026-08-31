package reports

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type reportCryptoGolden struct {
	Master        string `json:"master"`
	Connector     string `json:"connector"`
	BaseURL       string `json:"base_url"`
	Secret        string `json:"secret"`
	Note          string `json:"note"`
	CaseID        string `json:"case_id"`
	EndpointKeyID int64  `json:"endpoint_key_id"`
	Keys          struct {
		Fingerprint string `json:"fingerprint"`
		Idempotency string `json:"idempotency"`
		Target      string `json:"target"`
		SourceIP    string `json:"source_ip"`
		Rate        string `json:"rate"`
	} `json:"keys"`
	Fingerprint      string `json:"fingerprint"`
	RequestHash      string `json:"request_hash"`
	EmptyNoteRequest string `json:"empty_note_request_hash"`
	MaterialHash     string `json:"material_hash"`
	KeyRef           string `json:"key_ref"`
	IPv4Mapped       string `json:"ipv4_mapped"`
	IPv6             string `json:"ipv6"`
	IPv4Envelope     string `json:"ipv4_envelope"`
	IPv6Envelope     string `json:"ipv6_envelope"`
	Rates            struct {
		IP          string `json:"ip"`
		Account     string `json:"account"`
		Fingerprint string `json:"fingerprint"`
		Global      string `json:"global"`
	} `json:"rates"`
}

func loadReportCryptoGolden(t *testing.T) reportCryptoGolden {
	t.Helper()
	body, err := os.ReadFile("testdata/crypto_golden.json")
	if err != nil {
		t.Fatalf("read crypto golden: %v", err)
	}
	var fixture reportCryptoGolden
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatalf("decode crypto golden: %v", err)
	}
	clear(body)
	return fixture
}

func decodeGoldenBytes(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode golden hex: %v", err)
	}
	return decoded
}

func goldenArray32(t *testing.T, value string) [32]byte {
	t.Helper()
	decoded := decodeGoldenBytes(t, value)
	defer clear(decoded)
	if len(decoded) != 32 {
		t.Fatalf("golden digest length=%d", len(decoded))
	}
	var result [32]byte
	copy(result[:], decoded)
	return result
}

func goldenArray16(t *testing.T, value string) [16]byte {
	t.Helper()
	decoded := decodeGoldenBytes(t, value)
	defer clear(decoded)
	if len(decoded) != 16 {
		t.Fatalf("golden IP length=%d", len(decoded))
	}
	var result [16]byte
	copy(result[:], decoded)
	return result
}

func TestReportCryptoGoldenAndPurposeIsolation(t *testing.T) {
	fixture := loadReportCryptoGolden(t)
	master := decodeGoldenBytes(t, fixture.Master)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	random := decodeGoldenBytes(t, "000102030405060708090a0b0c0d0e0f1011121314151617")
	defer clear(random)
	keys, err := newReportKeys(vault, bytes.NewReader(random))
	if err != nil {
		t.Fatal(err)
	}

	expectedKeys := map[string][32]byte{
		"fingerprint": goldenArray32(t, fixture.Keys.Fingerprint),
		"idempotency": goldenArray32(t, fixture.Keys.Idempotency),
		"target":      goldenArray32(t, fixture.Keys.Target),
		"source_ip":   goldenArray32(t, fixture.Keys.SourceIP),
		"rate":        goldenArray32(t, fixture.Keys.Rate),
	}
	actualKeys := map[string][32]byte{
		"fingerprint": keys.fingerprint,
		"idempotency": keys.idempotency,
		"target":      keys.target,
		"source_ip":   keys.sourceIP,
		"rate":        keys.rate,
	}
	for name, expected := range expectedKeys {
		if actualKeys[name] != expected {
			t.Fatalf("%s subkey=%x want %x", name, actualKeys[name], expected)
		}
	}
	for name, first := range actualKeys {
		for otherName, second := range actualKeys {
			if name != otherName && first == second {
				t.Fatalf("purpose subkeys %s and %s alias", name, otherName)
			}
		}
	}

	fingerprint, err := keys.fingerprintDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret))
	if err != nil || fingerprint != goldenArray32(t, fixture.Fingerprint) {
		t.Fatalf("fingerprint=%x err=%v", fingerprint, err)
	}
	requestHash, err := keys.requestDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret), fixture.Note)
	if err != nil || requestHash != goldenArray32(t, fixture.RequestHash) {
		t.Fatalf("request hash=%x err=%v", requestHash, err)
	}
	emptyNote, err := keys.requestDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret), "")
	if err != nil || emptyNote != goldenArray32(t, fixture.EmptyNoteRequest) {
		t.Fatalf("empty-note request hash=%x err=%v", emptyNote, err)
	}
	materialHash, err := keys.materialDigest(fixture.CaseID, requestHash)
	if err != nil || materialHash != goldenArray32(t, fixture.MaterialHash) {
		t.Fatalf("material hash=%x err=%v", materialHash, err)
	}
	keyRef, err := keys.targetDigest(fixture.CaseID, fixture.EndpointKeyID)
	if err != nil || keyRef != goldenArray32(t, fixture.KeyRef) {
		t.Fatalf("key ref=%x err=%v", keyRef, err)
	}

	ipv4 := goldenArray16(t, fixture.IPv4Mapped)
	ipv6 := goldenArray16(t, fixture.IPv6)
	rateCases := []struct {
		scope    string
		value    []byte
		expected string
	}{
		{"ip", ipv4[:], fixture.Rates.IP},
		{"account", []byte("123"), fixture.Rates.Account},
		{"fingerprint", fingerprint[:], fixture.Rates.Fingerprint},
		{"global", nil, fixture.Rates.Global},
	}
	seenRates := make(map[[32]byte]string)
	for _, testCase := range rateCases {
		digest, err := keys.rateDigest(testCase.scope, testCase.value)
		if err != nil || digest != goldenArray32(t, testCase.expected) {
			t.Fatalf("rate %s=%x err=%v", testCase.scope, digest, err)
		}
		if previous, exists := seenRates[digest]; exists {
			t.Fatalf("rate scopes %s and %s alias", previous, testCase.scope)
		}
		seenRates[digest] = testCase.scope
	}

	ipv4Envelope, err := keys.sealSourceIP(fixture.CaseID, materialHash, ipv4)
	if err != nil || hex.EncodeToString(ipv4Envelope) != fixture.IPv4Envelope {
		t.Fatalf("IPv4 envelope=%x err=%v", ipv4Envelope, err)
	}
	ipv6Envelope, err := keys.sealSourceIP(fixture.CaseID, materialHash, ipv6)
	if err != nil || hex.EncodeToString(ipv6Envelope) != fixture.IPv6Envelope {
		t.Fatalf("IPv6 envelope=%x err=%v", ipv6Envelope, err)
	}
	openedIPv4, err := keys.openSourceIP(fixture.CaseID, materialHash, ipv4Envelope)
	if err != nil || openedIPv4 != ipv4 {
		t.Fatalf("open IPv4=%x err=%v", openedIPv4, err)
	}
	openedIPv6, err := keys.openSourceIP(fixture.CaseID, materialHash, ipv6Envelope)
	if err != nil || openedIPv6 != ipv6 {
		t.Fatalf("open IPv6=%x err=%v", openedIPv6, err)
	}

	if keys.String() != "[redacted report keys]" || keys.GoString() != "[redacted report keys]" {
		t.Fatal("report keys formatting is not redacted")
	}
	if err := keys.Close(); err != nil {
		t.Fatal(err)
	}
	if keys.fingerprint != ([32]byte{}) || keys.idempotency != ([32]byte{}) || keys.target != ([32]byte{}) ||
		keys.sourceIP != ([32]byte{}) || keys.rate != ([32]byte{}) {
		t.Fatal("Close did not clear report subkeys")
	}
	if _, err := keys.fingerprintDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret)); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed fingerprint error=%v", err)
	}
	if err := keys.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestReportCryptoRejectsCrossContextTamperAndGenerationChange(t *testing.T) {
	fixture := loadReportCryptoGolden(t)
	master := decodeGoldenBytes(t, fixture.Master)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	keys, err := newReportKeys(vault, bytes.NewReader(bytes.Repeat([]byte{0x44}, 24)))
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	requestHash, _ := keys.requestDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret), fixture.Note)
	materialHash, _ := keys.materialDigest(fixture.CaseID, requestHash)
	ip := goldenArray16(t, fixture.IPv4Mapped)
	envelope, err := keys.sealSourceIP(fixture.CaseID, materialHash, ip)
	if err != nil {
		t.Fatal(err)
	}

	otherCase := reportTestOpaqueID("rpc_", 9)
	if _, err := keys.openSourceIP(otherCase, materialHash, envelope); !errors.Is(err, errInvalidEnvelope) {
		t.Fatalf("cross-case error=%v", err)
	}
	otherMaterial := materialHash
	otherMaterial[0] ^= 0x80
	if _, err := keys.openSourceIP(fixture.CaseID, otherMaterial, envelope); !errors.Is(err, errInvalidEnvelope) {
		t.Fatalf("cross-material error=%v", err)
	}
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 1
	if _, err := keys.openSourceIP(fixture.CaseID, materialHash, tampered); !errors.Is(err, errInvalidEnvelope) {
		t.Fatalf("tamper error=%v", err)
	}
	wrongVersion := append([]byte(nil), envelope...)
	wrongVersion[0] = 2
	if _, err := keys.openSourceIP(fixture.CaseID, materialHash, wrongVersion); !errors.Is(err, errInvalidEnvelope) {
		t.Fatalf("version error=%v", err)
	}
	if _, err := keys.openSourceIP(fixture.CaseID, materialHash, envelope[:len(envelope)-1]); !errors.Is(err, errInvalidEnvelope) {
		t.Fatalf("length error=%v", err)
	}

	otherMaster := bytes.Repeat([]byte{0x91}, secret.MasterKeyBytes)
	otherVault, err := secret.New(otherMaster)
	clear(otherMaster)
	if err != nil {
		t.Fatal(err)
	}
	defer otherVault.Close()
	otherKeys, err := newReportKeys(otherVault, bytes.NewReader(bytes.Repeat([]byte{0x55}, 12)))
	if err != nil {
		t.Fatal(err)
	}
	defer otherKeys.Close()
	if _, err := otherKeys.openSourceIP(fixture.CaseID, materialHash, envelope); !errors.Is(err, errInvalidEnvelope) {
		t.Fatalf("generation-key error=%v", err)
	}
}

func TestReportDigestExactBytesAndNonceUniqueness(t *testing.T) {
	fixture := loadReportCryptoGolden(t)
	master := decodeGoldenBytes(t, fixture.Master)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	random := append(bytes.Repeat([]byte{0x01}, 12), bytes.Repeat([]byte{0x02}, 12)...)
	defer clear(random)
	keys, err := newReportKeys(vault, bytes.NewReader(random))
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()

	baseFingerprint, _ := keys.fingerprintDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret))
	variations := []struct {
		connector string
		baseURL   string
		secret    string
	}{
		{"anthropic-compatible", fixture.BaseURL, fixture.Secret},
		{fixture.Connector, "https://other.example/v1", fixture.Secret},
		{fixture.Connector, fixture.BaseURL, fixture.Secret + " "},
	}
	for _, variation := range variations {
		digest, err := keys.fingerprintDigest(variation.connector, variation.baseURL, []byte(variation.secret))
		if err != nil {
			t.Fatal(err)
		}
		if digest == baseFingerprint {
			t.Fatalf("fingerprint variation aliased: %+v", variation)
		}
	}
	composed, _ := keys.requestDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret), "é")
	decomposed, _ := keys.requestDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret), "e\u0301")
	if composed == decomposed {
		t.Fatal("request digest normalized Unicode")
	}
	if _, err := keys.rateDigest("account", []byte("01")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("leading-zero account rate error=%v", err)
	}
	if _, err := keys.rateDigest("ip", []byte{1, 2, 3}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("short IP rate error=%v", err)
	}
	if _, err := keys.rateDigest("unknown", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown rate scope error=%v", err)
	}

	requestHash, _ := keys.requestDigest(fixture.Connector, fixture.BaseURL, []byte(fixture.Secret), fixture.Note)
	materialHash, _ := keys.materialDigest(fixture.CaseID, requestHash)
	ip := goldenArray16(t, fixture.IPv4Mapped)
	first, err := keys.sealSourceIP(fixture.CaseID, materialHash, ip)
	if err != nil {
		t.Fatal(err)
	}
	second, err := keys.sealSourceIP(fixture.CaseID, materialHash, ip)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != envelopeBytes || len(second) != envelopeBytes || bytes.Equal(first[1:13], second[1:13]) || bytes.Equal(first, second) {
		t.Fatalf("nonces/envelopes are not unique: first=%x second=%x", first, second)
	}
}

type reportAliasDeriver struct {
	aliases   [][]byte
	duplicate bool
}

func (deriver *reportAliasDeriver) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
	var digest [32]byte
	if deriver.duplicate {
		digest = sha256.Sum256([]byte("duplicate"))
	} else {
		digest = sha256.Sum256(append([]byte("fixture:"), info...))
	}
	alias := append([]byte(nil), digest[:]...)
	deriver.aliases = append(deriver.aliases, alias)
	return alias, nil
}

func TestReportSubkeyAliasesAreClearedAndDuplicatesRejected(t *testing.T) {
	deriver := &reportAliasDeriver{}
	keys, err := newReportKeys(deriver, bytes.NewReader(bytes.Repeat([]byte{1}, 12)))
	if err != nil {
		t.Fatal(err)
	}
	for index, alias := range deriver.aliases {
		if !bytes.Equal(alias, make([]byte, len(alias))) {
			t.Fatalf("derived alias %d was not cleared: %x", index, alias)
		}
	}
	_ = keys.Close()

	duplicate := &reportAliasDeriver{duplicate: true}
	if _, err := newReportKeys(duplicate, bytes.NewReader(bytes.Repeat([]byte{1}, 12))); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate-purpose derivation error=%v", err)
	}
}
