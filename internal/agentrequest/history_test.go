package agentrequest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFullHistorySecretIsolationUsageAndCutoffCleanup(t *testing.T) {
	content := strings.Repeat("complete response\n", 10000)
	actualBodies := make(chan string, 3)
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		actualBodies <- string(raw)
		if r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			t.Error("dedicated key was not used")
		}
		answer(w, content+fixtureKey)
	})
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	first := submit(t, f.runtime, "history-first")
	result := waitResult(t, f.runtime, first.ID)
	if result.Usage.InputTokens != 12 || result.Usage.CachedInputTokens != 4 || result.Usage.OutputTokens != 3 || result.Usage.Unknown {
		t.Fatal(result.Usage)
	}
	detail, err := f.runtime.Detail(context.Background(), first.ID)
	if err != nil || detail.RequestBody != <-actualBodies || !detail.ResponseComplete || !detail.ContentRedacted || strings.Contains(detail.ResponseBody, fixtureKey) || !strings.Contains(detail.ResponseBody, "[redacted]") {
		t.Fatal("full history or redaction mismatch", err)
	}
	if len(detail.ResponseBody) < len(content) {
		t.Fatal("response truncated")
	}
	settings, err := f.runtime.Settings(context.Background())
	if err != nil || !settings.HasKey {
		t.Fatal(settings, err)
	}
	encoded, _ := json.Marshal(settings)
	if strings.Contains(string(encoded), fixtureKey) {
		t.Fatal("settings leaked key")
	}
	var stored string
	if err = f.database.QueryRow("SELECT encrypted_key FROM agent_request_settings").Scan(&stored); err != nil || strings.Contains(stored, fixtureKey) {
		t.Fatal("plaintext persisted", err)
	}
	malicious := input("secret-prompt")
	malicious.Messages = json.RawMessage("[{\"role\":\"user\",\"content\":\"" + fixtureKey + "\"}]")
	if _, err = f.runtime.Submit(context.Background(), malicious); !errors.Is(err, ErrInvalid) {
		t.Fatal("secret entered prompt", err)
	}
	page, err := f.runtime.List(context.Background(), ListFilter{Page: 1, PageSize: 25})
	if err != nil || page.CutoffID != first.ID {
		t.Fatal(page, err)
	}
	f.clock.Add(86400000)
	second := submit(t, f.runtime, "history-second")
	waitResult(t, f.runtime, second.ID)
	report, err := f.runtime.Usage(context.Background())
	if err != nil || len(report.Total) != 1 || report.Total[0].Requests != 2 || len(report.Last24Hours) != 1 || report.Last24Hours[0].Requests != 1 {
		t.Fatal("24h boundary", report, err)
	}
	skipped, err := f.runtime.Cleanup(context.Background(), CleanupInput{ThroughID: page.CutoffID})
	if err != nil || skipped.DeletedCount != 0 || skipped.SkippedCount != 1 {
		t.Fatal("unconsumed response was removed", skipped, err)
	}
	if _, err = f.database.Exec("CREATE TABLE workflow_results(request_id INTEGER PRIMARY KEY,body TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	tx, err := f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO workflow_results VALUES(?,?)", first.ID, result.RawResponse); err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.AckInTransaction(context.Background(), tx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	filtered, err := f.runtime.Cleanup(context.Background(), CleanupInput{ThroughID: page.CutoffID, State: "failed"})
	if err != nil || filtered.DeletedCount != 0 {
		t.Fatal("cleanup ignored selected state", filtered, err)
	}
	deleted, err := f.runtime.Cleanup(context.Background(), CleanupInput{ThroughID: page.CutoffID, State: "succeeded"})
	if err != nil || deleted.DeletedCount != 1 || deleted.SkippedCount != 0 {
		t.Fatal(deleted, err)
	}
	if _, err = f.runtime.Detail(context.Background(), second.ID); err != nil {
		t.Fatal("cutoff removed later request", err)
	}
	if _, err = f.runtime.Detail(context.Background(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var preserved string
	if err = f.database.QueryRow("SELECT body FROM workflow_results WHERE request_id=?", first.ID).Scan(&preserved); err != nil || preserved != result.RawResponse {
		t.Fatal("cleanup removed independent workflow", err)
	}
	report, err = f.runtime.Usage(context.Background())
	if err != nil || report.Total[0].Requests != 2 {
		t.Fatal("cleanup removed usage", report, err)
	}
}

func TestRestartPreservesPendingAndInterruptsUnknownWithoutReplay(t *testing.T) {
	called := make(chan string, 3)
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { called <- "network"; answer(w, "ok") })
	running := submit(t, f.runtime, "unknown")
	pending := submit(t, f.runtime, "pending")
	partial := []byte("{\"partial\":")
	if _, err := f.database.Exec("UPDATE agent_requests SET state='running',started_at=created_at,response_body=? WHERE id=?", partial, running.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	if got := await(t, called); got != "network" {
		t.Fatal(got)
	}
	waitResult(t, f.runtime, pending.ID)
	result, err := f.runtime.Wait(context.Background(), running.ID)
	if !errors.Is(err, ErrInterrupted) || result.RawResponse != string(partial) {
		t.Fatal("unknown replay/recovery", result, err)
	}
	select {
	case <-called:
		t.Fatal("unknown network call replayed")
	case <-time.After(25 * time.Millisecond):
	}
	report, err := f.runtime.Usage(context.Background())
	if err != nil || report.Total[0].Requests != 2 || !report.Total[0].Usage.Unknown {
		t.Fatal(report, err)
	}
}
func TestLowerConcurrencyDrainsAndRejoinsFIFO(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{}, 3)
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		started <- "start"
		select {
		case <-release:
			answer(w, "ok")
		case <-r.Context().Done():
		}
	})
	s, err := f.runtime.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	putDriver(t, f.runtime, s.BaseURL, 2)
	a := submit(t, f.runtime, "one")
	b := submit(t, f.runtime, "two")
	c := submit(t, f.runtime, "three")
	if err = f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	await(t, started)
	await(t, started)
	putDriver(t, f.runtime, s.BaseURL, 1)
	release <- struct{}{}
	// One of the two earlier calls completes; the remaining call retains the
	// lowered capacity until its response is fully consumed.
	select {
	case <-started:
		t.Fatal("lowered limit started another call")
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	waitResult(t, f.runtime, a.ID)
	waitResult(t, f.runtime, b.ID)
	await(t, started)
	release <- struct{}{}
	waitResult(t, f.runtime, c.ID)
}

type fragmentedReader struct{ io.Reader }

func (r fragmentedReader) Read(body []byte) (int, error) {
	if len(body) > 7 {
		body = body[:7]
	}
	return r.Reader.Read(body)
}

func TestResponseChunksPreservePartialBytesWithoutSecretFragments(t *testing.T) {
	f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network") })
	request := submit(t, f.runtime, "chunks")
	if _, err := f.database.Exec("UPDATE agent_requests SET state='running',started_at=created_at WHERE id=?", request.ID); err != nil {
		t.Fatal(err)
	}
	raw := []byte(strings.Repeat("a", 32760) + fixtureKey + strings.Repeat("b", 65536))
	received, err := f.runtime.readResponse(context.Background(), request.ID, fragmentedReader{strings.NewReader(string(raw))}, []byte(fixtureKey))
	if err != nil || string(received) != string(raw) {
		t.Fatal("transport bytes changed", err)
	}
	stored, redacted, err := readResponseChunks(context.Background(), f.database, request.ID)
	expected, _ := sanitizeSecret(raw, []byte(fixtureKey))
	if err != nil || !redacted || string(stored) != string(expected) {
		t.Fatal("split secret/chunk preservation", err)
	}
	var count, bodyLength int
	if err = f.database.QueryRow("SELECT (SELECT COUNT(*) FROM agent_request_response_chunks WHERE request_id=?),length(response_body) FROM agent_requests WHERE id=?", request.ID, request.ID).Scan(&count, &bodyLength); err != nil || (count < 2 || count > 3) || bodyLength != 0 {
		t.Fatal("response prefixes were rewritten", count, bodyLength, err)
	}
	if err = f.runtime.recoverInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail, err := f.runtime.Detail(context.Background(), request.ID)
	if err != nil || detail.ResponseBody != string(expected) || detail.ResponseComplete || !detail.ContentRedacted {
		t.Fatal("durable partial recovery", err)
	}
	tx, err := f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.AckInTransaction(context.Background(), tx, request.ID); err != nil {
		t.Fatal("interrupted result cannot be acknowledged", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestMissingUsageIsUnknownAndPendingCannotBeAcknowledged(t *testing.T) {
	result, err := parseResult(1, []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	if err != nil || !result.Usage.Unknown {
		t.Fatal(result.Usage, err)
	}
	f := newFixture(t, func(http.ResponseWriter, *http.Request) {})
	request := submit(t, f.runtime, "not-complete")
	tx, err := f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.AckInTransaction(context.Background(), tx, request.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("pending acknowledgment accepted", err)
	}
	tx.Rollback()
	if err = f.runtime.Cancel(context.Background(), request.ID); err != nil {
		t.Fatal(err)
	}
	tx, err = f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.AckInTransaction(context.Background(), tx, request.ID); err != nil {
		t.Fatal("cancelled acknowledgment denied", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestUsageSeparatesStewardsAfterLogCleanupAndActorDeletion(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { answer(w, "ok") })
	if _, err := f.database.Exec("CREATE TABLE users(id INTEGER PRIMARY KEY);INSERT INTO users VALUES(5),(6)"); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for index, actor := range []int64{5, 6, 5} {
		in := input([]string{"first-five", "six", "next-five"}[index])
		in.Source = "steward_chat"
		in.ActorID = actor
		in.ActorRole = "level5"
		in.ActorLabel = "Historical steward"
		item, err := f.runtime.Submit(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		waitResult(t, f.runtime, item.ID)
		ids = append(ids, strconv.FormatInt(item.ID, 10))
		tx, err := f.database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err = f.runtime.AckInTransaction(context.Background(), tx, item.ID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.database.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	removed, err := f.runtime.Cleanup(context.Background(), CleanupInput{IDs: ids})
	if err != nil || removed.DeletedCount != 3 {
		t.Fatal(removed, err)
	}
	report, err := f.runtime.Usage(context.Background())
	if err != nil || len(report.Total) != 2 || len(report.Last24Hours) != 2 {
		t.Fatal(report, err)
	}
	if report.Total[0].ActorID != 5 || report.Total[0].Requests != 2 || report.Total[1].ActorID != 6 || report.Total[1].Requests != 1 || report.Total[0].ActorLabel != "Historical steward" {
		t.Fatal(report.Total)
	}
}

func TestOutputTokenParameterCannotConflictWithExtraParameters(t *testing.T) {
	s := defaultSettings()
	s.Model = "driver"
	s.OutputTokenParameter = "max_completion_tokens"
	body, err := requestBody(input("parameters"), s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err = json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["max_tokens"]; ok {
		t.Fatal("both output token parameters were sent")
	}
	if string(decoded["max_completion_tokens"]) != "4096" {
		t.Fatal(string(body))
	}
	s.ExtraParameters = json.RawMessage(`{"max_tokens":1}`)
	if _, err = requestBody(input("parameters"), s); !errors.Is(err, ErrInvalid) {
		t.Fatal("conflicting output override accepted", err)
	}
}

func TestDiscoverModelsBeforeSelectingDriverModel(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/models" {
			t.Error("wrong discovery request", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"data":[{"id":"b"},{"id":"a"},{"id":"a"}]}`)
	})
	s, err := f.runtime.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.runtime.PutSettings(context.Background(), SettingsUpdate{ExpectedRevision: s.Revision, BaseURL: s.BaseURL, ContextWindow: s.ContextWindow, MaxOutputTokens: s.MaxOutputTokens, Concurrency: 1, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	models, err := f.runtime.DiscoverModels(context.Background())
	if err != nil || len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatal(models, err)
	}
	current, _, _ := f.flow.SampleGlobalRPM()
	if current != 0 {
		t.Fatal("configuration discovery counted as model inference", current)
	}
	for _, raw := range []string{"01", "+1", "0", "-1", "9223372036854775808"} {
		if _, err = f.runtime.Cleanup(context.Background(), CleanupInput{IDs: []string{raw}}); !errors.Is(err, ErrInvalid) {
			t.Fatal("noncanonical cleanup ID accepted", raw, err)
		}
	}
}
func TestInvalidUTF8ErrorHasExactOriginalBytes(t *testing.T) {
	raw := []byte{0xff, 0, 0xfe}
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502); w.Write(raw) })
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	request := submit(t, f.runtime, "binary-error")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.runtime.Wait(ctx, request.ID); !errors.Is(err, ErrUpstream) {
		t.Fatal(err)
	}
	detail, err := f.runtime.Detail(ctx, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(detail.ResponseBodyBase64)
	if err != nil || !bytes.Equal(decoded, raw) || !detail.ResponseComplete {
		t.Fatal("error bytes lost", detail, err)
	}
}

func TestAdminWriteReauthorizationAndUnconsumedRecovery(t *testing.T) {
	f := newFixture(t, func(http.ResponseWriter, *http.Request) {})
	request := submit(t, f.runtime, "job:42:1")
	pending, err := f.runtime.Unconsumed(context.Background(), 10)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	if err = f.runtime.Cancel(context.Background(), request.ID); err != nil {
		t.Fatal(err)
	}
	waiting, err := f.runtime.Unconsumed(context.Background(), 10)
	if err != nil || len(waiting) != 1 || waiting[0].ClientRequestID != "job:42:1" {
		t.Fatal("orphan call cannot be recovered", waiting, err)
	}
	denied := errors.New("administrator session revoked")
	f.runtime.adminAuthorize = func(context.Context, *sql.Tx) error { return denied }
	s, err := f.runtime.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.runtime.PutSettings(context.Background(), SettingsUpdate{ExpectedRevision: s.Revision, BaseURL: s.BaseURL, Model: s.Model, ContextWindow: s.ContextWindow, MaxOutputTokens: s.MaxOutputTokens, Concurrency: 2, TimeoutSeconds: 10})
	if !errors.Is(err, denied) {
		t.Fatal("configuration skipped final authorization", err)
	}
	if _, err = f.runtime.Cleanup(context.Background(), CleanupInput{ThroughID: request.ID}); !errors.Is(err, denied) {
		t.Fatal("cleanup skipped final authorization", err)
	}
	tx, err := f.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.runtime.AckInTransaction(context.Background(), tx, request.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	waiting, err = f.runtime.Unconsumed(context.Background(), 10)
	if err != nil || len(waiting) != 0 {
		t.Fatal(waiting, err)
	}
}

func TestHTTPFailureStillAccountsReportedUsage(t *testing.T) {
	f := newFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"processing failed"},"usage":{"prompt_tokens":12,"completion_tokens":1}}`)
	})
	if err := f.runtime.Start(); err != nil {
		t.Fatal(err)
	}
	request := submit(t, f.runtime, "usage-on-error")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := f.runtime.Wait(ctx, request.ID)
	if !errors.Is(err, ErrUpstream) || result.Usage.Unknown || result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 1 {
		t.Fatal(result.Usage, err)
	}
	report, err := f.runtime.Usage(ctx)
	if err != nil || len(report.Total) != 1 || report.Total[0].Usage.OutputTokens != 1 {
		t.Fatal(report, err)
	}
}

func TestQueuedInputSurvivesCancellationAndDiscoveryRechecksAdmin(t *testing.T) {
	f := newFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("revoked model discovery reached network") })
	request := submit(t, f.runtime, "full-queued-prompt")
	if err := f.runtime.Cancel(context.Background(), request.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := f.runtime.Detail(context.Background(), request.ID)
	if err != nil || detail.RequestBody != "" {
		t.Fatal("cancelled unsent call fabricated wire body", detail, err)
	}
	var saved Input
	if err = json.Unmarshal(detail.QueuedInput, &saved); err != nil || saved.ClientRequestID != "full-queued-prompt" || string(saved.Messages) != string(input("full-queued-prompt").Messages) {
		t.Fatal("queued prompt was lost", string(detail.QueuedInput), err)
	}
	denied := errors.New("admin revoked while awaiting dispatch")
	f.runtime.adminAuthorize = func(context.Context, *sql.Tx) error { return denied }
	if _, err = f.runtime.DiscoverModels(context.Background()); !errors.Is(err, denied) {
		t.Fatal("discovery did not preserve live authorization denial", err)
	}
}
