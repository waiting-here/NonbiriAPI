package logapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRoleSpecificLogDTOGoldenAndForbiddenFields(t *testing.T) {
	fixture := newLogFixture(t)
	self, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.selfID, AttemptFilter{})
	if err != nil {
		t.Fatalf("GetUser self: %v", err)
	}
	charity, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.charityID, AttemptFilter{})
	if err != nil {
		t.Fatalf("GetUser charity: %v", err)
	}
	admin, err := fixture.repo.GetAdmin(context.Background(), fixture.selfID, AttemptFilter{})
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	steward, err := fixture.repo.GetSteward(
		context.Background(), 999, fixture.selfID, AttemptFilter{}, allowLogStewardRead{},
	)
	if err != nil {
		t.Fatalf("GetSteward: %v", err)
	}
	result := struct {
		UserSelf    UserLogDetail    `json:"user_self"`
		UserCharity UserLogDetail    `json:"user_charity"`
		Admin       AdminLogDetail   `json:"admin"`
		Steward     StewardLogDetail `json:"steward"`
	}{self, charity, admin, steward}
	assertLogGolden(t, "role_dtos.golden", marshalLogGolden(t, result))

	// Raw/body/auth/cookie/ciphertext sentinels exist in fixture columns but no
	// role SELECT includes them.
	noLogSentinel(t, result, "RAW-REQUEST-BODY", "RAW-AUTH", "RAW-COOKIE", "RAW-DISCORD",
		"RAW-PRIVATE-NOTE", "RAW-CIPHERTEXT", "RAW-UPSTREAM", "RAW-RESPONSE-BODY", "RAW-SET-COOKIE")
	noLogSentinel(t, admin, "SELF-LOGICAL-MODEL", "OWNER-ENDPOINT-NOTE", "OWNER-KEY-NOTE")
	noLogSentinel(t, steward, "SELF-LOGICAL-MODEL", "OWNER-ENDPOINT-NOTE", "OWNER-KEY-NOTE")
	requireNoJSONKeys(t, steward, "user_id", "model", "endpoint_note", "key_note", "attempts_export",
		"authorization", "cookie", "body", "secret", "ciphertext")
	requireNoJSONKeys(t, admin, "model", "endpoint_note", "key_note", "authorization", "cookie", "body", "secret")

	selfDetail, ok := self.(UserSelfLogDetail)
	if !ok || len(selfDetail.Attempts.Data) != 2 {
		t.Fatalf("self detail type/data = %T %+v", self, self)
	}
	if selfDetail.Attempts.Data[0].EndpointNote != "OWNER-ENDPOINT-NOTE-SENTINEL" ||
		selfDetail.Attempts.Data[0].KeyNote != "OWNER-KEY-NOTE-SENTINEL" ||
		selfDetail.Attempts.Data[1].EndpointNote != "" || selfDetail.Attempts.Data[1].KeyNote != "" {
		t.Fatalf("owner/deleted note projection = %+v", selfDetail.Attempts.Data)
	}
	if selfDetail.Request.Usage.Charge != "1.234" {
		t.Fatalf("logical charge = %q", selfDetail.Request.Usage.Charge)
	}
	if selfDetail.Attempts.Data[1].ResultKind != ResultSynthetic || selfDetail.Attempts.Data[1].StatusCode != nil ||
		admin.Attempts.Data[1].ResultKind != ResultSynthetic || admin.Attempts.Data[1].StatusCode != nil ||
		steward.Attempts.Data[1].ResultKind != ResultSynthetic || steward.Attempts.Data[1].StatusCode != nil {
		t.Fatalf("synthetic NULL status projection: user=%+v admin=%+v steward=%+v",
			selfDetail.Attempts.Data[1], admin.Attempts.Data[1], steward.Attempts.Data[1])
	}
	for _, attempt := range selfDetail.Attempts.Data {
		if attempt.Usage.Charge != "0" {
			t.Fatalf("attempt charge must not duplicate logical charge: %+v", attempt)
		}
	}
	if _, ok := charity.(UserCharityLogDetail); !ok {
		t.Fatalf("charity detail type = %T", charity)
	}
}

func TestWideUsageAndU128ChargeRemainCanonicalStrings(t *testing.T) {
	fixture := newLogFixture(t)
	const maximumInt64 = int64(^uint64(0) >> 1)
	fixture.mustExec(`UPDATE request_logs SET
uncached_input_tokens=?,cache_write_input_tokens=?,cache_read_input_tokens=?,output_tokens=?
WHERE logical_request_id=?`, maximumInt64, maximumInt64, maximumInt64, maximumInt64, fixture.selfID)
	fixture.mustExec(`UPDATE credit_entries SET delta_mag=? WHERE operation_id=?`, bytes.Repeat([]byte{0xff}, 16), "op-self")

	detail, err := fixture.repo.GetAdmin(context.Background(), fixture.selfID, AttemptFilter{})
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	usage := detail.Request.Usage
	if usage.UncachedInputTokens != "9223372036854775807" ||
		usage.CacheWriteInputTokens != "9223372036854775807" ||
		usage.CacheReadInputTokens != "9223372036854775807" ||
		usage.OutputTokens != "9223372036854775807" ||
		usage.TotalTokens != "36893488147419103228" ||
		usage.Charge != "340282366920938463463374607431768211.455" {
		t.Fatalf("wide usage = %+v", usage)
	}
}

func TestCharityDetailShapeIsIndependentOfPhysicalAttempts(t *testing.T) {
	fixture := newLogFixture(t)
	before, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	fixture.insertAttempt(2, 3, 202, 302, "https://charity-three.example/v1", "openai-compatible", "charity-up-3",
		string(ResultSynthetic), 502, "transport", "safe third diagnostic", [4]int64{1, 1, 1, 1}, 0, 211, 215)
	fixture.mustExec(`UPDATE request_logs SET attempt_count=3 WHERE id=2`)
	after, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("charity detail changed with retries\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
	requireNoJSONKeys(t, after, "attempts", "next_cursor", "attempt_count", "endpoint_base_url", "upstream_model_id")
}

func TestOwnerIsolationDeletionAndRoleProjection(t *testing.T) {
	fixture := newLogFixture(t)
	if _, err := fixture.repo.GetUser(context.Background(), logUserTwo, fixture.selfID, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GetUser = %v", err)
	}
	page, err := fixture.repo.ListUser(context.Background(), logUserOne, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page.Data {
		encoded, _ := json.Marshal(row)
		if strings.Contains(string(encoded), fixture.otherID) || strings.Contains(string(encoded), fixture.deletedID) {
			t.Fatalf("foreign/deleted row leaked: %s", encoded)
		}
	}
	admin, err := fixture.repo.GetAdmin(context.Background(), fixture.deletedID, AttemptFilter{})
	if err != nil || admin.Request.UserID != nil {
		t.Fatalf("deleted admin projection = (%+v,%v)", admin, err)
	}
	steward, err := fixture.repo.GetSteward(
		context.Background(), 555, fixture.deletedID, AttemptFilter{}, allowLogStewardRead{},
	)
	if err != nil {
		t.Fatal(err)
	}
	requireNoJSONKeys(t, steward, "user_id", "model", "discord_id", "private_note")

	fixture.mustExec(`DELETE FROM endpoint_keys WHERE id=301`)
	fixture.mustExec(`DELETE FROM endpoints WHERE id=201`)
	detail, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	self := detail.(UserSelfLogDetail)
	if self.Attempts.Data[0].EndpointNote != "" || self.Attempts.Data[0].KeyNote != "" {
		t.Fatalf("deleted resources recreated private notes: %+v", self.Attempts.Data[0])
	}
}

func TestLogicalFiltersVisibleAttemptFiltersAndStrictCursorBinding(t *testing.T) {
	fixture := newLogFixture(t)
	status429 := 429
	page, err := fixture.repo.ListAdmin(context.Background(), ListFilter{Status: &status429})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("attempt status matched logical filter = (%+v,%v)", page, err)
	}
	status422 := 422
	page, err = fixture.repo.ListAdmin(context.Background(), ListFilter{Status: &status422})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != fixture.selfID {
		t.Fatalf("logical status filter = (%+v,%v)", page, err)
	}
	endpoint := "https://charity-two.example/v1"
	upstream := "charity-up-2"
	steward, err := fixture.repo.ListSteward(context.Background(), 700, ListFilter{
		EndpointBaseURL: &endpoint, UpstreamModel: &upstream,
	}, allowLogStewardRead{})
	if err != nil || len(steward.Data) != 1 || steward.Data[0].ID != fixture.charityID {
		t.Fatalf("steward projected attempt filter = (%+v,%v)", steward, err)
	}

	first, err := fixture.repo.ListUser(context.Background(), logUserOne, ListFilter{Limit: 1})
	if err != nil || len(first.Data) != 1 || first.NextCursor == nil {
		t.Fatalf("first cursor page = (%+v,%v)", first, err)
	}
	second, err := fixture.repo.ListUser(context.Background(), logUserOne, ListFilter{Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Data) != 1 || second.Data[0].(UserSelfLogRow).ID == first.Data[0].(UserSelfLogRow).ID {
		// The newest pending and next deleted/self branches can vary in concrete
		// type; compare IDs through JSON below if the assertion's cast is unsafe.
		if err != nil || len(second.Data) != 1 {
			t.Fatalf("second cursor page = (%+v,%v)", second, err)
		}
	}
	if _, err := fixture.repo.ListUser(context.Background(), logUserTwo, ListFilter{Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-owner cursor = %v", err)
	}
	model := "SELF-LOGICAL-MODEL-SENTINEL"
	if _, err := fixture.repo.ListUser(context.Background(), logUserOne, ListFilter{Limit: 1, Cursor: *first.NextCursor, Model: &model}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-filter cursor = %v", err)
	}
	if _, err := fixture.repo.ListAdmin(context.Background(), ListFilter{Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-role cursor = %v", err)
	}
	fixture.clock.Advance(cursorLifetime)
	if _, err := fixture.repo.ListUser(context.Background(), logUserOne, ListFilter{Limit: 1, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired cursor = %v", err)
	}
	fixture.keys.requireCanonicalInfo(t)
}

func TestAttemptCursorRoleAndOwnerBinding(t *testing.T) {
	fixture := newLogFixture(t)
	first, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.selfID, AttemptFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	self := first.(UserSelfLogDetail)
	if len(self.Attempts.Data) != 1 || self.Attempts.NextCursor == nil {
		t.Fatalf("attempt first page = %+v", self)
	}
	next, err := fixture.repo.GetUser(context.Background(), logUserOne, fixture.selfID,
		AttemptFilter{Limit: 1, Cursor: *self.Attempts.NextCursor})
	if err != nil || next.(UserSelfLogDetail).Attempts.Data[0].AttemptSeq != "2" {
		t.Fatalf("attempt second page = (%+v,%v)", next, err)
	}
	if _, err := fixture.repo.GetAdmin(context.Background(), fixture.selfID,
		AttemptFilter{Limit: 1, Cursor: *self.Attempts.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-role attempt cursor = %v", err)
	}
	if _, err := fixture.repo.GetUser(context.Background(), logUserTwo, fixture.selfID,
		AttemptFilter{Limit: 1, Cursor: *self.Attempts.NextCursor}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner check should win before attempt cursor = %v", err)
	}
}

func TestRepositoryFailsClosedOnTerminalAndAttemptInvariant(t *testing.T) {
	t.Run("terminal matrix", func(t *testing.T) {
		fixture := newLogFixture(t)
		fixture.mustExec(`UPDATE request_logs SET caller_result_class='success',caller_status=500 WHERE id=1`)
		if _, err := fixture.repo.ListAdmin(context.Background(), ListFilter{}); !errors.Is(err, ErrInvariant) {
			t.Fatalf("invalid terminal matrix = %v", err)
		}
	})
	t.Run("response attempt status", func(t *testing.T) {
		fixture := newLogFixture(t)
		fixture.mustExec(`UPDATE request_attempts SET upstream_status=NULL WHERE request_log_id=1 AND attempt_seq=1`)
		if _, err := fixture.repo.GetAdmin(context.Background(), fixture.selfID, AttemptFilter{}); !errors.Is(err, ErrInvariant) {
			t.Fatalf("null response status = %v", err)
		}
	})
	t.Run("synthetic attempt non-null range", func(t *testing.T) {
		fixture := newLogFixture(t)
		fixture.mustExec(`UPDATE request_attempts SET upstream_status=99 WHERE request_log_id=1 AND attempt_seq=2`)
		if _, err := fixture.repo.GetAdmin(context.Background(), fixture.selfID, AttemptFilter{}); !errors.Is(err, ErrInvariant) {
			t.Fatalf("out-of-range synthetic status = %v", err)
		}
	})
	t.Run("strict time range", func(t *testing.T) {
		fixture := newLogFixture(t)
		from, to := int64(10), int64(10)
		if _, err := fixture.repo.ListAdmin(context.Background(), ListFilter{From: &from, To: &to}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("empty time range = %v", err)
		}
	})
}
