package reports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
)

func reportPublicBody(connectorType, baseURL, secretValue string, note *string) []byte {
	body := map[string]any{
		"connector_type": connectorType,
		"base_url":       baseURL,
		"secret":         secretValue,
	}
	if note != nil {
		body["note"] = *note
	}
	encoded, _ := json.Marshal(body)
	return encoded
}

func invokeAnonymousReport(
	repository *Repository,
	body []byte,
	idempotencyKey string,
	contentType string,
	query string,
) *httptest.ResponseRecorder {
	target := "https://user.example" + publicRoute + query
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.RemoteAddr = "192.0.2.44:32000"
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	repository.publicReportHTTP(recorder, request, nil)
	return recorder
}

func assertAcceptedWire(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(recorder.Body.Bytes(), acceptedResponseBody) {
		t.Fatalf("body=%q want %q", recorder.Body.Bytes(), acceptedResponseBody)
	}
	wantHeaders := http.Header{
		"Cache-Control":             []string{"no-store"},
		"Content-Type":              []string{"application/json; charset=utf-8"},
		"X-Nonbiri-Report-Accepted": []string{"1"},
	}
	if !reflect.DeepEqual(recorder.Header(), wantHeaders) {
		t.Fatalf("headers=%v want %v", recorder.Header(), wantHeaders)
	}
}

func TestPublicReportHitAndMissHaveEquivalentWireAndNoSecretPersistence(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "matching-credential", reportTestNow)
	note := "administrator-only note"

	hit := invokeAnonymousReport(environment.repository,
		reportPublicBody(reportTestConnector, " HTTPS://EXAMPLE.COM:443//v1 ", "matching-credential", &note),
		reportTestKey(1), "application/json; charset=utf-8", "")
	miss := invokeAnonymousReport(environment.repository,
		reportPublicBody(reportTestConnector, reportTestBaseURL, "not-present", &note),
		reportTestKey(2), "application/json", "")
	assertAcceptedWire(t, hit)
	assertAcceptedWire(t, miss)
	if !reflect.DeepEqual(hit.Header(), miss.Header()) || !bytes.Equal(hit.Body.Bytes(), miss.Body.Bytes()) {
		t.Fatal("hit and miss public responses differ")
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases`); got != 1 {
		t.Fatalf("case count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_materials`); got != 1 {
		t.Fatalf("material count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions`); got != 1 {
		t.Fatalf("suspension count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`); got != 2 {
		t.Fatalf("acceptance rail count=%d", got)
	}
	var responseBody []byte
	if err := environment.store.DB().QueryRow(`SELECT response_body FROM idempotency_records
WHERE scope='credential_report' ORDER BY created_at,key_hash LIMIT 1`).Scan(&responseBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(responseBody, acceptedResponseBody) {
		t.Fatalf("persisted replay body=%q", responseBody)
	}
	for _, secretValue := range []string{"matching-credential", "not-present"} {
		if bytes.Contains(responseBody, []byte(secretValue)) {
			t.Fatalf("replay body contains structured secret %q", secretValue)
		}
	}
	var envelope []byte
	if err := environment.store.DB().QueryRow(`SELECT source_ip_envelope FROM report_materials`).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != envelopeBytes || bytes.Contains(envelope, []byte("192.0.2.44")) {
		t.Fatalf("source IP envelope=%x", envelope)
	}
}

type repeatedByteReader byte

func (reader repeatedByteReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = byte(reader)
	}
	return len(destination), nil
}

func TestPublicReportHitAndMissTimingWindow(t *testing.T) {
	environment := newReportTestEnvironmentWith(t, reportTestOptions{
		random: repeatedByteReader(0), delay: defaultDelay,
	})
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "timing-hit", reportTestNow)
	note := "timing"

	type measured struct {
		recorder *httptest.ResponseRecorder
		duration time.Duration
	}
	measure := func(secretValue string, key int) measured {
		started := time.Now()
		recorder := invokeAnonymousReport(environment.repository,
			reportPublicBody(reportTestConnector, reportTestBaseURL, secretValue, &note),
			reportTestKey(key), "application/json", "")
		return measured{recorder: recorder, duration: time.Since(started)}
	}
	hit := measure("timing-hit", 10)
	miss := measure("timing-miss", 11)
	assertAcceptedWire(t, hit.recorder)
	assertAcceptedWire(t, miss.recorder)
	for name, result := range map[string]measured{"hit": hit, "miss": miss} {
		if result.duration < 340*time.Millisecond || result.duration > 900*time.Millisecond {
			t.Fatalf("%s duration=%s outside padded window tolerance", name, result.duration)
		}
	}
	if difference := hit.duration - miss.duration; difference > 250*time.Millisecond || difference < -250*time.Millisecond {
		t.Fatalf("hit/miss timing diverged: hit=%s miss=%s", hit.duration, miss.duration)
	}
}

func TestAcceptanceDurationUsesExactClosedInterval(t *testing.T) {
	environment := newReportTestEnvironmentWith(t, reportTestOptions{
		random: bytes.NewReader([]byte{0, 0, 0, 150}),
	})
	minimum, err := environment.repository.acceptanceDuration()
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := environment.repository.acceptanceDuration()
	if err != nil {
		t.Fatal(err)
	}
	if minimum != 350*time.Millisecond || maximum != 500*time.Millisecond {
		t.Fatalf("acceptance duration minimum=%s maximum=%s", minimum, maximum)
	}
}

func TestPublicReportStrictBodyAndPreLookupRejections(t *testing.T) {
	environment := newReportTestEnvironment(t)
	note := "note"
	valid := reportPublicBody(reportTestConnector, reportTestBaseURL, "valid-secret", &note)
	invalidUTF8 := append([]byte(nil), valid...)
	invalidUTF8[len(invalidUTF8)-2] = 0xff
	oversized := []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"s","note":"` +
		strings.Repeat("x", maxPublicRequestBodyBytes) + `"}`)
	tests := []struct {
		name         string
		body         []byte
		key          string
		contentType  string
		query        string
		wantStatus   int
		duplicateKey bool
	}{
		{"empty", nil, reportTestKey(20), "application/json", "", http.StatusBadRequest, false},
		{"missing fields", []byte(`{}`), reportTestKey(21), "application/json", "", http.StatusBadRequest, false},
		{"unknown field", []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"s","note":"","extra":1}`), reportTestKey(22), "application/json", "", http.StatusBadRequest, false},
		{"duplicate field", []byte(`{"connector_type":"openai-compatible","connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"s","note":""}`), reportTestKey(23), "application/json", "", http.StatusBadRequest, false},
		{"trailing JSON", append(append([]byte(nil), valid...), []byte(` {}`)...), reportTestKey(24), "application/json", "", http.StatusBadRequest, false},
		{"secret wrong type", []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":7,"note":""}`), reportTestKey(25), "application/json", "", http.StatusBadRequest, false},
		{"isolated surrogate", []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"\ud800","note":""}`), reportTestKey(26), "application/json", "", http.StatusBadRequest, false},
		{"note NUL", []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"s","note":"prefix\u0000suffix"}`), reportTestKey(27), "application/json", "", http.StatusBadRequest, false},
		{"note newline", []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"s","note":"prefix\nsuffix"}`), reportTestKey(270), "application/json", "", http.StatusBadRequest, false},
		{"note DEL", []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"s","note":"prefix\u007fsuffix"}`), reportTestKey(271), "application/json", "", http.StatusBadRequest, false},
		{"unknown connector", reportPublicBody("unknown", reportTestBaseURL, "s", &note), reportTestKey(28), "application/json", "", http.StatusBadRequest, false},
		{"empty connector", reportPublicBody("", reportTestBaseURL, "s", &note), reportTestKey(29), "application/json", "", http.StatusBadRequest, false},
		{"invalid base", reportPublicBody(reportTestConnector, "https://invalid.example", "s", &note), reportTestKey(30), "application/json", "", http.StatusBadRequest, false},
		{"missing idempotency", valid, "", "application/json", "", http.StatusBadRequest, false},
		{"short idempotency", valid, "short", "application/json", "", http.StatusBadRequest, false},
		{"duplicate idempotency", valid, reportTestKey(31), "application/json", "", http.StatusBadRequest, true},
		{"query", valid, reportTestKey(32), "application/json", "?x=1", http.StatusBadRequest, false},
		{"wrong media type", valid, reportTestKey(33), "text/plain", "", http.StatusBadRequest, false},
		{"invalid utf8", invalidUTF8, reportTestKey(34), "application/json", "", http.StatusBadRequest, false},
		{"payload too large", oversized, reportTestKey(35), "application/json", "", http.StatusRequestEntityTooLarge, false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			target := "https://user.example" + publicRoute + testCase.query
			request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(testCase.body))
			request.RemoteAddr = "192.0.2.50:1234"
			if testCase.contentType != "" {
				request.Header.Set("Content-Type", testCase.contentType)
			}
			if testCase.key != "" {
				request.Header.Set("Idempotency-Key", testCase.key)
				if testCase.duplicateKey {
					request.Header.Add("Idempotency-Key", reportTestKey(99))
				}
			}
			recorder := httptest.NewRecorder()
			environment.repository.publicReportHTTP(recorder, request, nil)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`); got != 0 {
				t.Fatalf("rejected request wrote %d acceptance rows", got)
			}
			if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_rate_buckets`); got != 0 {
				t.Fatalf("rejected request wrote %d rate rows", got)
			}
		})
	}

	getWithBody := httptest.NewRequest(http.MethodGet, "https://admin.example"+adminBadgeRoute, strings.NewReader(`{}`))
	getRecorder := httptest.NewRecorder()
	environment.repository.badgeHTTP(getRecorder, getWithBody)
	if getRecorder.Code != http.StatusBadRequest {
		t.Fatalf("GET body status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	getWithoutBody := httptest.NewRequest(http.MethodGet, "https://admin.example"+adminBadgeRoute, nil)
	getRecorder = httptest.NewRecorder()
	environment.repository.badgeHTTP(getRecorder, getWithoutBody)
	if getRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("bodyless unauthenticated GET status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
}

func adminMutationTestRequest(body string) (*httptest.ResponseRecorder, *http.Request) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://admin.example/admin/api/reports/rpc_AAAAAAAAAAAAAAAAAAAAAQ/action", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return recorder, request
}

func TestAdminMutationVersionsRequireCanonicalDecimalStrings(t *testing.T) {
	const maximum = "9223372036854775807"

	recorder, request := adminMutationTestRequest(`{"expected_material_version":"9223372036854775807","expected_target_version":"9223372036854775807","reason":"confirmed report","confirmation":true}`)
	approve, err := readAdminApproveCommand(recorder, request, reportTestKey(260))
	if err != nil || approve.ExpectedMaterialVersion != 9223372036854775807 || approve.ExpectedTargetVersion != 9223372036854775807 {
		t.Fatalf("valid approve command=%+v err=%v", approve, err)
	}
	recorder, request = adminMutationTestRequest(`{"expected_material_version":"1","expected_target_version":"9223372036854775807","reason":"rejected report"}`)
	reject, err := readAdminRejectCommand(recorder, request, reportTestKey(261))
	if err != nil || reject.ExpectedMaterialVersion != 1 || reject.ExpectedTargetVersion != 9223372036854775807 {
		t.Fatalf("valid reject command=%+v err=%v", reject, err)
	}
	recorder, request = adminMutationTestRequest(`{"expected_target_version":"9223372036854775807"}`)
	resume, err := readAdminResumeCommand(recorder, request, reportTestKey(262))
	if err != nil || resume.ExpectedTargetVersion != 9223372036854775807 {
		t.Fatalf("valid resume command=%+v err=%v", resume, err)
	}
	if parsed, err := parseReportRevision(maximum); err != nil || parsed != 9223372036854775807 {
		t.Fatalf("maximum revision=%d err=%v", parsed, err)
	}

	environment := newReportTestEnvironment(t)
	tests := []struct {
		name string
		body string
		read func(http.ResponseWriter, *http.Request) error
	}{
		{
			name: "approve material JSON number",
			body: `{"expected_material_version":1,"expected_target_version":"1","reason":"confirmed report","confirmation":true}`,
			read: func(writer http.ResponseWriter, request *http.Request) error {
				_, err := readAdminApproveCommand(writer, request, reportTestKey(263))
				return err
			},
		},
		{
			name: "approve target JSON number",
			body: `{"expected_material_version":"1","expected_target_version":1,"reason":"confirmed report","confirmation":true}`,
			read: func(writer http.ResponseWriter, request *http.Request) error {
				_, err := readAdminApproveCommand(writer, request, reportTestKey(264))
				return err
			},
		},
		{
			name: "reject JSON number",
			body: `{"expected_material_version":"1","expected_target_version":1,"reason":"rejected report"}`,
			read: func(writer http.ResponseWriter, request *http.Request) error {
				_, err := readAdminRejectCommand(writer, request, reportTestKey(265))
				return err
			},
		},
		{
			name: "resume JSON number",
			body: `{"expected_target_version":1}`,
			read: func(writer http.ResponseWriter, request *http.Request) error {
				_, err := readAdminResumeCommand(writer, request, reportTestKey(266))
				return err
			},
		},
	}
	for _, value := range []string{"", "0", "01", "+1", "1.0", "9223372036854775808", "１２"} {
		value := value
		tests = append(tests, struct {
			name string
			body string
			read func(http.ResponseWriter, *http.Request) error
		}{
			name: "resume noncanonical " + value,
			body: fmt.Sprintf(`{"expected_target_version":%q}`, value),
			read: func(writer http.ResponseWriter, request *http.Request) error {
				_, err := readAdminResumeCommand(writer, request, reportTestKey(267))
				return err
			},
		})
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder, request := adminMutationTestRequest(testCase.body)
			if err := testCase.read(recorder, request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error=%v", err)
			}
			if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='control_mutation'`); got != 0 {
				t.Fatalf("invalid wire wrote %d idempotency rows", got)
			}
			if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_decisions`); got != 0 {
				t.Fatalf("invalid wire wrote %d decisions", got)
			}
		})
	}
}

func TestPublicReportRequestBodyLimit(t *testing.T) {
	environment := newReportTestEnvironment(t)
	note := "body limit"
	body := reportPublicBody(reportTestConnector, reportTestBaseURL, "body-limit-secret", &note)
	if len(body) >= maxPublicRequestBodyBytes {
		t.Fatalf("fixture body unexpectedly large: %d", len(body))
	}
	atLimit := append(append([]byte(nil), body...), bytes.Repeat([]byte{' '}, maxPublicRequestBodyBytes-len(body))...)
	if len(atLimit) != maxPublicRequestBodyBytes {
		t.Fatalf("at-limit body bytes=%d", len(atLimit))
	}
	accepted := invokeAnonymousReport(environment.repository, atLimit, reportTestKey(36), "application/json", "")
	assertAcceptedWire(t, accepted)

	overLimit := append(append([]byte(nil), atLimit...), ' ')
	rejected := invokeAnonymousReport(environment.repository, overLimit, reportTestKey(37), "application/json", "")
	if rejected.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("80 KiB+1 status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`); got != 1 {
		t.Fatalf("body-limit replay rows=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_rate_buckets`); got != 3 {
		t.Fatalf("body-limit rate rows=%d", got)
	}
}

func TestPublicReportEscapedControlSecretsAreAcceptedAndDigestExact(t *testing.T) {
	environment := newReportTestEnvironment(t)
	note := "control digest"
	tests := []struct {
		name   string
		body   []byte
		secret []byte
	}{
		{
			name:   "nul",
			body:   []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"prefix\u0000suffix","note":"control digest"}`),
			secret: []byte("prefix\x00suffix"),
		},
		{
			name:   "newline",
			body:   []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"prefix\nsuffix","note":"control digest"}`),
			secret: []byte("prefix\nsuffix"),
		},
		{
			name:   "del",
			body:   []byte(`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","secret":"prefix\u007fsuffix","note":"control digest"}`),
			secret: []byte("prefix\x7fsuffix"),
		},
	}
	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if !validSecret(testCase.secret) {
				t.Fatal("decoded control secret was rejected")
			}
			key := reportTestKey(38 + index)
			response := invokeAnonymousReport(environment.repository, testCase.body, key, "application/json", "")
			assertAcceptedWire(t, response)

			keyHash := sha256.Sum256([]byte(key))
			var storedRequest, storedFingerprint []byte
			if err := environment.store.DB().QueryRow(`SELECT request_hash,lookup_fingerprint
FROM idempotency_records WHERE scope='credential_report' AND key_hash=?`, keyHash[:]).Scan(
				&storedRequest, &storedFingerprint,
			); err != nil {
				t.Fatal(err)
			}
			wantRequest, err := environment.repository.keys.requestDigest(
				reportTestConnector, reportTestBaseURL, testCase.secret, note,
			)
			if err != nil {
				t.Fatal(err)
			}
			wantFingerprint, err := environment.repository.keys.fingerprintDigest(
				reportTestConnector, reportTestBaseURL, testCase.secret,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(storedRequest, wantRequest[:]) || !bytes.Equal(storedFingerprint, wantFingerprint[:]) {
				t.Fatalf("control secret digest changed: request=%x fingerprint=%x", storedRequest, storedFingerprint)
			}
			plainFingerprint, err := environment.repository.keys.fingerprintDigest(
				reportTestConnector, reportTestBaseURL, []byte("prefixsuffix"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if plainFingerprint == wantFingerprint {
				t.Fatal("control bytes were omitted from fingerprint")
			}
		})
	}
}

func TestPublicReportMissingAndExplicitEmptyNoteAreOneReplay(t *testing.T) {
	environment := newReportTestEnvironment(t)
	bodyMissing := reportPublicBody(reportTestConnector, reportTestBaseURL, "empty-note-secret", nil)
	empty := ""
	bodyEmpty := reportPublicBody(reportTestConnector, reportTestBaseURL, "empty-note-secret", &empty)
	key := reportTestKey(40)
	first := invokeAnonymousReport(environment.repository, bodyMissing, key, "application/json", "")
	second := invokeAnonymousReport(environment.repository, bodyEmpty, key, "application/json", "")
	assertAcceptedWire(t, first)
	assertAcceptedWire(t, second)
	if got := environment.rowCount(t, `SELECT SUM(count) FROM report_rate_buckets`); got != 3 {
		t.Fatalf("replay consumed rate buckets: sum=%d", got)
	}
}

func TestDedicatedAcceptanceReplayConflictAndWindowBoundaries(t *testing.T) {
	environment := newReportTestEnvironment(t)
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "replay-secret", reportTestNow)
	originalSecret := []byte("replay-secret")
	submission := environment.submission("replay-secret", "first", 50, 1, nil)
	submission.Secret = originalSecret
	if err := environment.repository.AcceptCredentialTheft(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalSecret, make([]byte, len(originalSecret))) {
		t.Fatalf("structured secret was not cleared: %x", originalSecret)
	}
	environment.accept(t, "replay-secret", "first", 50, 1, nil)
	conflictingSecret := []byte("replay-secret")
	conflicting := environment.submission("replay-secret", "different", 50, 2, nil)
	conflicting.Secret = conflictingSecret
	if err := environment.repository.AcceptCredentialTheft(context.Background(), conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("different replay error=%v", err)
	}
	if !bytes.Equal(conflictingSecret, make([]byte, len(conflictingSecret))) {
		t.Fatal("conflicting structured secret was not cleared")
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_materials`); got != 1 {
		t.Fatalf("material count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT SUM(count) FROM report_rate_buckets`); got != 3 {
		t.Fatalf("replay/conflict changed rate buckets: sum=%d", got)
	}

	var createdAt, expiresAt int64
	var keyHash, requestHash, lookupFingerprint, responseBody []byte
	if err := environment.store.DB().QueryRow(`SELECT key_hash,request_hash,lookup_fingerprint,response_body,created_at,expires_at
FROM idempotency_records WHERE scope='credential_report'`).Scan(
		&keyHash, &requestHash, &lookupFingerprint, &responseBody, &createdAt, &expiresAt,
	); err != nil {
		t.Fatal(err)
	}
	wantKeyHash := sha256.Sum256([]byte(reportTestKey(50)))
	if !bytes.Equal(keyHash, wantKeyHash[:]) || len(requestHash) != 32 || len(lookupFingerprint) != 32 ||
		!bytes.Equal(responseBody, acceptedResponseBody) || createdAt != reportTestNow || expiresAt != reportTestNow+replayWindowSeconds {
		t.Fatalf("invalid replay row: key=%x request=%x lookup=%x created=%d expires=%d body=%q",
			keyHash, requestHash, lookupFingerprint, createdAt, expiresAt, responseBody)
	}

	environment.setNow(reportTestNow + replayWindowSeconds - 1)
	if err := environment.repository.AcceptCredentialTheft(context.Background(),
		environment.submission("window-change", "different", 50, 3, nil)); !errors.Is(err, ErrConflict) {
		t.Fatalf("window -1 conflict error=%v", err)
	}
	environment.setNow(reportTestNow + replayWindowSeconds)
	if err := environment.repository.AcceptCredentialTheft(context.Background(),
		environment.submission("window-change", "different", 50, 3, nil)); err != nil {
		t.Fatalf("window equality should be a new submission: %v", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT created_at,expires_at FROM idempotency_records
WHERE scope='credential_report'`).Scan(&createdAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if createdAt != reportTestNow+replayWindowSeconds || expiresAt != reportTestNow+2*replayWindowSeconds {
		t.Fatalf("renewed fixed window created=%d expires=%d", createdAt, expiresAt)
	}

	t.Run("one second after expiry is also new", func(t *testing.T) {
		late := newReportTestEnvironment(t)
		late.accept(t, "initial-window", "", 51, 1, nil)
		late.setNow(reportTestNow + replayWindowSeconds + 1)
		late.accept(t, "changed-after-window", "changed", 51, 2, nil)
		var renewedAt, renewedExpiry int64
		if err := late.store.DB().QueryRow(`SELECT created_at,expires_at FROM idempotency_records
WHERE scope='credential_report'`).Scan(&renewedAt, &renewedExpiry); err != nil {
			t.Fatal(err)
		}
		if renewedAt != reportTestNow+replayWindowSeconds+1 || renewedExpiry != renewedAt+replayWindowSeconds {
			t.Fatalf("late renewal created=%d expires=%d", renewedAt, renewedExpiry)
		}
	})
}

func TestReportRateBoundaries(t *testing.T) {
	t.Run("IP", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		for index := 0; index < 5; index++ {
			environment.accept(t, fmt.Sprintf("ip-secret-%d", index), "", 100+index, 1, nil)
		}
		deniedKey := 199
		err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("ip-secret-denied", "", deniedKey, 1, nil))
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("IP limit error=%v", err)
		}
		if got := environment.rowCount(t, `SELECT count FROM report_rate_buckets WHERE scope='ip'`); got != 5 {
			t.Fatalf("IP bucket count=%d", got)
		}
		assertNoAcceptanceKey(t, environment, deniedKey)
		environment.setNow(reportTestNow + 599)
		if err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("ip-secret-denied", "", deniedKey, 1, nil)); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("IP window -1 error=%v", err)
		}
		environment.setNow(reportTestNow + 600)
		environment.accept(t, "ip-secret-denied", "", deniedKey, 1, nil)
	})

	t.Run("fingerprint", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		for index := 0; index < 3; index++ {
			environment.accept(t, "same-fingerprint", fmt.Sprintf("note-%d", index), 200+index, index, nil)
		}
		deniedKey := 299
		err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("same-fingerprint", "note-denied", deniedKey, 9, nil))
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("fingerprint limit error=%v", err)
		}
		if got := environment.rowCount(t, `SELECT count FROM report_rate_buckets WHERE scope='fingerprint'`); got != 3 {
			t.Fatalf("fingerprint bucket count=%d", got)
		}
		assertNoAcceptanceKey(t, environment, deniedKey)
		environment.setNow(reportTestNow + 599)
		if err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("same-fingerprint", "note-denied", deniedKey, 9, nil)); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("fingerprint window -1 error=%v", err)
		}
		environment.setNow(reportTestNow + 600)
		environment.accept(t, "same-fingerprint", "note-denied", deniedKey, 9, nil)
	})

	t.Run("account", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		actor := environment.seedActor(t, false, 1)
		for index := 0; index < 10; index++ {
			environment.accept(t, fmt.Sprintf("account-secret-%d", index), "", 300+index, index, &actor)
		}
		deniedKey := 399
		err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("account-secret-denied", "", deniedKey, 20, &actor))
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("account limit error=%v", err)
		}
		if got := environment.rowCount(t, `SELECT count FROM report_rate_buckets WHERE scope='account'`); got != 10 {
			t.Fatalf("account bucket count=%d", got)
		}
		assertNoAcceptanceKey(t, environment, deniedKey)
		environment.setNow(reportTestNow + 599)
		if err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("account-secret-denied", "", deniedKey, 20, &actor)); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("account window -1 error=%v", err)
		}
		environment.setNow(reportTestNow + 600)
		environment.accept(t, "account-secret-denied", "", deniedKey, 20, &actor)
	})

	t.Run("global", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		for index := 0; index < 64; index++ {
			environment.accept(t, fmt.Sprintf("global-secret-%d", index), "", 400+index, index, nil)
		}
		deniedKey := 499
		err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("global-secret-denied", "", deniedKey, 100, nil))
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("global limit error=%v", err)
		}
		if got := environment.rowCount(t, `SELECT count FROM report_rate_buckets WHERE scope='global'`); got != 64 {
			t.Fatalf("global bucket count=%d", got)
		}
		assertNoAcceptanceKey(t, environment, deniedKey)
		environment.setNow(reportTestNow + 59)
		if err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("global-secret-denied", "", deniedKey, 100, nil)); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("global window -1 error=%v", err)
		}
		environment.setNow(reportTestNow + 60)
		environment.accept(t, "global-secret-denied", "", deniedKey, 100, nil)
	})
}

func assertNoAcceptanceKey(t *testing.T, environment *reportTestEnvironment, keySequence int) {
	t.Helper()
	keyHash := sha256.Sum256([]byte(reportTestKey(keySequence)))
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records
WHERE scope='credential_report' AND key_hash=?`, keyHash[:]); got != 0 {
		t.Fatalf("rejected acceptance key persisted %d rows", got)
	}
}

type blockingReportDelay struct {
	entered chan struct{}
	release chan struct{}
}

func (delay *blockingReportDelay) wait(ctx context.Context, _ time.Duration) error {
	select {
	case delay.entered <- struct{}{}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	select {
	case <-delay.release:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func TestPublicReportConcurrencyGateAndSameFingerprintConvergence(t *testing.T) {
	t.Run("concurrency gate", func(t *testing.T) {
		delay := &blockingReportDelay{entered: make(chan struct{}, publicConcurrency), release: make(chan struct{})}
		environment := newReportTestEnvironmentWith(t, reportTestOptions{delay: delay.wait})
		results := make(chan error, publicConcurrency)
		for index := 0; index < publicConcurrency; index++ {
			index := index
			go func() {
				results <- environment.repository.AcceptCredentialTheft(context.Background(),
					environment.submission(fmt.Sprintf("slot-secret-%d", index), "", 500+index, index, nil))
			}()
		}
		for index := 0; index < publicConcurrency; index++ {
			select {
			case <-delay.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out filling public report slots")
			}
		}
		if err := environment.repository.AcceptCredentialTheft(context.Background(),
			environment.submission("slot-overflow", "", 599, 99, nil)); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("overflow error=%v", err)
		}
		close(delay.release)
		for index := 0; index < publicConcurrency; index++ {
			if err := <-results; err != nil {
				t.Fatalf("admitted call %d: %v", index, err)
			}
		}
	})

	t.Run("same fingerprint", func(t *testing.T) {
		environment := newReportTestEnvironment(t)
		owner := environment.seedActor(t, false, 1)
		endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
		environment.seedEndpointKey(t, endpointID, "concurrent-fingerprint", reportTestNow)
		start := make(chan struct{})
		results := make(chan error, 8)
		for index := 0; index < 8; index++ {
			index := index
			go func() {
				<-start
				results <- environment.repository.AcceptCredentialTheft(context.Background(),
					environment.submission("concurrent-fingerprint", fmt.Sprintf("material-%d", index), 600+index, index, nil))
			}()
		}
		close(start)
		accepted, limited := 0, 0
		for index := 0; index < 8; index++ {
			switch err := <-results; {
			case err == nil:
				accepted++
			case errors.Is(err, ErrRateLimited):
				limited++
			default:
				t.Fatalf("concurrent acceptance error=%v", err)
			}
		}
		if accepted != 3 || limited != 5 {
			t.Fatalf("accepted=%d limited=%d", accepted, limited)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases
WHERE status IN ('pending_indexing','pending_review','approved_processing')`); got != 1 {
			t.Fatalf("active case count=%d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_materials`); got != 3 {
			t.Fatalf("material count=%d", got)
		}
		if got := environment.rowCount(t, `SELECT COUNT(*) FROM endpoint_key_suspensions`); got != 1 {
			t.Fatalf("suspension count=%d", got)
		}
	})
}

func TestPublicReportCancellationBoundaries(t *testing.T) {
	var delayCalls atomic.Int64
	entered := make(chan struct{}, 1)
	delay := func(ctx context.Context, _ time.Duration) error {
		if delayCalls.Add(1) == 1 {
			entered <- struct{}{}
			<-ctx.Done()
			return context.Cause(ctx)
		}
		return context.Cause(ctx)
	}
	environment := newReportTestEnvironmentWith(t, reportTestOptions{delay: delay})
	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "cancel-secret", reportTestNow)

	cancelled, cancelImmediately := context.WithCancel(context.Background())
	cancelImmediately()
	if err := environment.repository.AcceptCredentialTheft(cancelled,
		environment.submission("pre-cancel", "", 700, 1, nil)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pre-cancel error=%v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`); got != 0 {
		t.Fatalf("pre-cancel wrote %d rows", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	secretBytes := []byte("cancel-secret")
	submission := environment.submission("cancel-secret", "", 701, 2, nil)
	submission.Secret = secretBytes
	result := make(chan error, 1)
	go func() { result <- environment.repository.AcceptCredentialTheft(ctx, submission) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("acceptance did not reach cancellable padding")
	}
	cancel()
	if err := <-result; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("padding cancellation error=%v", err)
	}
	if !bytes.Equal(secretBytes, make([]byte, len(secretBytes))) {
		t.Fatal("cancelled acceptance did not clear secret")
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`); got != 1 {
		t.Fatalf("committed acceptance row count=%d", got)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases`); got != 1 {
		t.Fatalf("committed case count=%d", got)
	}
	if err := environment.repository.AcceptCredentialTheft(context.Background(),
		environment.submission("cancel-secret", "", 701, 2, nil)); err != nil {
		t.Fatalf("retry after cancellation: %v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_materials`); got != 1 {
		t.Fatalf("retry duplicated materials: %d", got)
	}
}

type reportRouteRegistration struct {
	method  string
	pattern string
}

type reportTestRegistrar struct {
	optional []reportRouteRegistration
	admin    []reportRouteRegistration
}

func (registrar *reportTestRegistrar) RegisterOptionalUserRoute(method, pattern string, _ auth.OptionalUserHandler) error {
	registrar.optional = append(registrar.optional, reportRouteRegistration{method, pattern})
	return nil
}

func (registrar *reportTestRegistrar) RegisterAdminRoute(method, pattern string, _ http.Handler) error {
	registrar.admin = append(registrar.admin, reportRouteRegistration{method, pattern})
	return nil
}

func TestReportRouteRegistrationUsesOptionalUserAndAdminOnlySurfaces(t *testing.T) {
	environment := newReportTestEnvironment(t)
	registrar := &reportTestRegistrar{}
	if err := environment.repository.RegisterRoutes(registrar); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(registrar.optional, []reportRouteRegistration{{http.MethodPost, publicRoute}}) {
		t.Fatalf("optional routes=%v", registrar.optional)
	}
	wantAdmin := []reportRouteRegistration{
		{http.MethodGet, adminBadgeRoute},
		{http.MethodGet, adminCasesRoute},
		{http.MethodGet, adminCaseRoute},
		{http.MethodGet, adminTargetsRoute},
		{http.MethodPost, adminApproveRoute},
		{http.MethodPost, adminRejectRoute},
		{http.MethodPost, adminResumeRoute},
	}
	if !reflect.DeepEqual(registrar.admin, wantAdmin) {
		t.Fatalf("admin routes=%v want=%v", registrar.admin, wantAdmin)
	}
	for _, route := range append(append([]reportRouteRegistration(nil), registrar.optional...), registrar.admin...) {
		if strings.Contains(route.pattern, "/steward") {
			t.Fatalf("registered L5 report route %q", route.pattern)
		}
	}
	var nilRegistrar *reportTestRegistrar
	if err := environment.repository.RegisterRoutes(nilRegistrar); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("typed-nil registrar error=%v", err)
	}
}

func TestLoggedReportFinalAuthorizationAndPrivacy(t *testing.T) {
	environment := newReportTestEnvironment(t)
	reporter := environment.seedActor(t, false, 1)
	environment.accept(t, "logged-miss", "private", 800, 1, &reporter)
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM report_cases`); got != 0 {
		t.Fatalf("logged miss created %d cases", got)
	}

	owner := environment.seedActor(t, false, 1)
	endpointID := environment.seedEndpoint(t, owner.UserID, reportTestConnector, reportTestBaseURL)
	environment.seedEndpointKey(t, endpointID, "logged-hit", reportTestNow)
	environment.accept(t, "logged-hit", "private", 801, 2, &reporter)
	var reporterUserID int64
	var reporterDiscordID string
	if err := environment.store.DB().QueryRow(`SELECT reporter_user_id,reporter_discord_id FROM report_materials`).Scan(
		&reporterUserID, &reporterDiscordID,
	); err != nil {
		t.Fatal(err)
	}
	if reporterUserID != reporter.UserID || reporterDiscordID == "" {
		t.Fatalf("reporter projection user=%d discord=%q", reporterUserID, reporterDiscordID)
	}

	if _, err := environment.store.DB().Exec(`DELETE FROM sessions WHERE token_hash=?`, reporter.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	err := environment.repository.AcceptCredentialTheft(context.Background(),
		environment.submission("stale-session", "", 802, 3, &reporter))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale reporter authorization error=%v", err)
	}
	if got := environment.rowCount(t, `SELECT COUNT(*) FROM idempotency_records WHERE scope='credential_report'`); got != 2 {
		t.Fatalf("stale session changed acceptance rows: %d", got)
	}

	admin := environment.seedActor(t, true, 1)
	if _, err := environment.repository.Badge(context.Background(), admin); err != nil {
		t.Fatalf("admin badge: %v", err)
	}
	steward := environment.seedActor(t, false, 5)
	if _, err := environment.repository.Badge(context.Background(), steward); !errors.Is(err, ErrForbidden) {
		t.Fatalf("L5 badge error=%v", err)
	}
	if _, err := environment.repository.Badge(context.Background(), owner); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary user badge error=%v", err)
	}
}

func TestPublicSubmissionStringAndLimits(t *testing.T) {
	environment := newReportTestEnvironment(t)
	secretBytes := bytes.Repeat([]byte{'s'}, maxSecretBytes)
	submission := environment.submission(string(secretBytes), strings.Repeat("界", maxNoteRunes), 900, 1, nil)
	if submission.String() != "[redacted credential report submission]" || submission.GoString() != "[redacted credential report submission]" {
		t.Fatal("submission formatting is not redacted")
	}
	if err := environment.repository.AcceptCredentialTheft(context.Background(), submission); err != nil {
		t.Fatalf("boundary submission: %v", err)
	}
	tooLongSecret := environment.submission(string(append(secretBytes, 'x')), "", 901, 2, nil)
	if err := environment.repository.AcceptCredentialTheft(context.Background(), tooLongSecret); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized secret error=%v", err)
	}
	tooManyRunes := environment.submission("s", strings.Repeat("a", maxNoteRunes+1), 902, 3, nil)
	if err := environment.repository.AcceptCredentialTheft(context.Background(), tooManyRunes); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized note error=%v", err)
	}
}
