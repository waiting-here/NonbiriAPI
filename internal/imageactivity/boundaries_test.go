package imageactivity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestPhysicalIdentityWindowPersistsAndOldControlsRemainVisible(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	before, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	f.tx(t, func(tx *sql.Tx) {
		ok, _, err := reserveHTTPTx(context.Background(), tx, before.Control.ID, f.now.Load()*1000)
		if err != nil || !ok {
			t.Fatal(err)
		}
	})
	input := f.settings
	input.ExpectedRevision = before.Revision
	input.Secret = SecretInput{Mode: "keep"}
	input.RPM = 1
	if _, err = f.service.PutUpstream(f.ctx(f.admin), f.admin, f.key(), input); err != nil {
		t.Fatal(err)
	}
	same, err := f.service.GetUpstream(f.ctx(f.admin), f.admin)
	if err != nil || same.Control.ID != before.Control.ID {
		t.Fatalf("identity reset %v", err)
	}
	f.tx(t, func(tx *sql.Tx) {
		ok, retry, e := reserveHTTPTx(context.Background(), tx, before.Control.ID, f.now.Load()*1000)
		if e != nil || ok || retry != (f.now.Load()+60)*1000 {
			t.Fatalf("RPM reset ok=%v retry=%d %v", ok, retry, e)
		}
	})
	f.tx(t, func(tx *sql.Tx) {
		if e := pauseControlTx(context.Background(), tx, before.Control.ID, "receipt_unknown", f.now.Load()); e != nil {
			t.Fatal(e)
		}
	})
	input.ExpectedRevision = same.Revision
	replacement := "different-fixture-secret"
	input.Secret = SecretInput{Mode: "replace", Value: &replacement}
	if _, err = f.service.PutUpstream(f.ctx(f.admin), f.admin, f.key(), input); err != nil {
		t.Fatal(err)
	}
	controls, err := f.service.Controls(f.ctx(f.admin), f.admin, 100, "")
	if err != nil || len(controls.Data) != 2 {
		t.Fatalf("old controls %+v %v", controls, err)
	}
	found := false
	for _, row := range controls.Data {
		if row.ID == before.Control.ID {
			found = !row.Current && row.Paused
		}
	}
	if !found {
		t.Fatal("old protected identity undiscoverable")
	}
	f.now.Add(60)
	f.tx(t, func(tx *sql.Tx) {
		ok, _, e := reserveHTTPTx(context.Background(), tx, before.Control.ID, f.now.Load()*1000)
		if e != nil || !ok {
			t.Fatalf("window boundary %v %v", ok, e)
		}
	})
}
func TestFIFOResourceBudgetAndQueuedExpiry(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	first := f.submit(t, f.user, 1)
	second := f.submit(t, f.other, 1)
	// Two retained results can occupy the configured budget; admissions must
	// wait at the head without evicting those promised pickup windows.
	memory := &f.service.memory
	memory.mu.Lock()
	memory.used += (512 << 20)
	memory.mu.Unlock()
	if err := f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.upstream.posts.Load() != 0 {
		t.Fatal("RAM ceiling ignored")
	}
	f.now.Add(60)
	if err := f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, err := f.service.GetTask(f.ctx(f.user), f.user, first.ID)
	if err != nil || a.Status != "cancelled" || *a.ErrorCode != "queue_timeout" {
		t.Fatalf("head expiry %+v %v", a, err)
	}
	b, err := f.service.GetTask(f.ctx(f.other), f.other, second.ID)
	if err != nil || b.Status != "cancelled" || b.ErrorCode == nil || *b.ErrorCode != "queue_timeout" {
		t.Fatalf("later deadline ignored %+v %v", b, err)
	}
	memory.mu.Lock()
	memory.used -= (512 << 20)
	memory.mu.Unlock()
	if err = f.service.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.checkLedger(t)
}
func TestMemoryReaderAndRetiredExecutionKeepReservation(t *testing.T) {
	m := memoryStore{items: map[string]*memoryItem{}}
	if !m.reserveQueued("task", 1, []byte("prompt"), 512<<20) || !m.commitQueued("task") || !m.prepareExecution("task", 512<<20) {
		t.Fatal("reservation")
	}
	m.purge("task")
	if m.used != executionMemory {
		t.Fatal("active memory released before reader completed")
	}
	if m.publish("task", 1, []imageBytes{{[]byte("image"), "image/png"}}, 100) {
		t.Fatal("retired result resurrected")
	}
	m.discard("task")
	if m.used != 0 || m.queued != 0 {
		t.Fatal("active reservation leak")
	}
	if !m.reserveQueued("cached", 1, []byte("prompt"), 512<<20) || !m.commitQueued("cached") || !m.prepareExecution("cached", 512<<20) {
		t.Fatal("reservation")
	}
	if !m.publish("cached", 1, []imageBytes{{[]byte("image"), "image/png"}}, 100) {
		t.Fatal("publish")
	}
	img, release, ok := m.image("cached", 1, 99, 0)
	if !ok {
		t.Fatal("borrow")
	}
	m.expire(100)
	if m.used != 5 || string(img.data) != "image" {
		t.Fatal("borrowed data no longer charged")
	}
	if _, _, ok = m.image("cached", 1, 99, 0); ok {
		t.Fatal("retired cache served again")
	}
	release()
	release()
	if m.used != 0 {
		t.Fatal("reader release leak")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestBoundedResponseAllocationAndReaderFailure(t *testing.T) {
	// Disabling GC makes TotalAlloc delta a conservative bound for every
	// simultaneous response copy, independently of collection scheduling.
	runtime.GC()
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	body, err := readBounded(io.LimitReader(zeroReader{}, maxResponse+1), maxResponse)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrCapacity) || len(body) != maxResponse {
		t.Fatalf("bound %d %v", len(body), err)
	}
	if used := after.TotalAlloc - before.TotalAlloc; used > uint64(executionMemory) {
		t.Fatalf("response copies exceed reservation %d", used)
	}
	runtime.KeepAlive(body)
	malformed := []byte(`{"status":"done","data":[{"b64":"incomplete"}]`)
	if _, err = responseState(malformed, ResponseAdapter{StatePointer: ptr("/status"), SuccessStates: []string{"done"}}); err == nil {
		t.Fatal("truncated terminal accepted")
	}
	duplicate := []byte(`{"status":"done","status":"pending","data":[]}`)
	if _, err = responseState(duplicate, ResponseAdapter{StatePointer: ptr("/status"), SuccessStates: []string{"done"}, WorkingStates: []string{"pending"}}); err == nil {
		t.Fatal("ambiguous terminal accepted")
	}
}
func TestPrivateAdapterAndMediaBoundaries(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	for _, path := range []string{"https://other.invalid/generate", "//other.invalid/generate", "/jobs?id={task_id}", "/jobs/{task_id}/{task_id}", "/jobs/{task_id}#fragment"} {
		if validRelativePath(path, true) {
			t.Fatalf("unsafe poll path %s", path)
		}
	}
	target, err := requestURL("https://upstream.example.invalid", "/jobs/{task_id}", "../secret?a=b")
	if err != nil || !strings.Contains(target, "%2E%2E%2F") {
		t.Fatalf("unencoded task ID %s %v", target, err)
	}
	snapshot := upstreamSnapshot{origins: []string{"https://images.example.invalid"}}
	for _, value := range []string{"http://127.0.0.1/private", "https://user:secret@images.example.invalid/a", "https://images.example.invalid/a#bad", "https://unlisted.example.invalid/a"} {
		if _, err = downloadTarget(value, snapshot); err == nil {
			t.Fatalf("unsafe image URL %s", value)
		}
	}
	if _, err = checkedImage(f.upstream.png, "image/jpeg"); err == nil {
		t.Fatal("MIME mismatch accepted")
	}
	if _, err = checkedImage([]byte("<svg/>"), "image/svg+xml"); err == nil {
		t.Fatal("SVG accepted")
	}
	if _, err = checkedImage(f.upstream.png[:len(f.upstream.png)-12], "image/png"); err == nil {
		t.Fatal("truncated PNG accepted")
	}
	raw := `"` + base64.StdEncoding.EncodeToString(f.upstream.png) + `"`
	decoded, err := decodeImageBase64([]byte(raw))
	if err != nil || !bytes.Equal(decoded, f.upstream.png) {
		t.Fatalf("base64 %v", err)
	}
	webp := make([]byte, 30)
	copy(webp, "RIFF")
	binary.LittleEndian.PutUint32(webp[4:8], 22)
	copy(webp[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(webp[16:20], 10)
	if _, err = checkedImage(webp, "image/webp"); err == nil {
		t.Fatal("header-only WebP accepted")
	}
}
func TestHTTPStrictInputsAndSafeBinaryPickup(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	for _, body := range []string{`{"model_id":"a","model_id":"b","prompt":"x"}`, `{"prompt":"x","unknown":1}`, `null`, strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33)} {
		request := httptest.NewRequest("POST", userPrefix+"/tasks", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		var input SubmitInput
		if decodeInput(request, &input, maxJSON, []string{"model_id", "expected_model_revision", "prompt"}) == nil {
			t.Fatalf("invalid input accepted %s", body)
		}
	}
	task := f.submit(t, f.user, 1)
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "succeeded"
	})
	request := httptest.NewRequest("GET", userPrefix+"/tasks/"+task.ID+"/images/0", nil).WithContext(f.ctx(f.user))
	request.SetPathValue("id", task.ID)
	request.SetPathValue("index", "0")
	response := httptest.NewRecorder()
	f.service.serveImage(response, request, UserPrincipal{UserID: f.user})
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(response.Body.Bytes(), f.upstream.png) {
		t.Fatalf("pickup %d", response.Code)
	}
	catalog, err := f.service.ListModels(f.ctx(f.admin), f.admin, true, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(catalog)
	if bytes.Contains(raw, []byte(`"parameters":null`)) || bytes.Contains(raw, []byte(`"combinations":null`)) {
		t.Fatal("nil public collections")
	}
}
func TestDiagnosticsRetentionReleasesBudgetAndPreservesLedger(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	f.upstream.mode.Store(3)
	task := f.submit(t, f.user, 1)
	f.wait(t, func() bool {
		r, e := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
		return e == nil && r.Status == "failed"
	})
	var count int
	if err := f.database.QueryRow("SELECT count(*) FROM request_error_bodies WHERE task_id=?", task.ID).Scan(&count); err != nil || count == 0 {
		t.Fatalf("missing raw diagnostic %d %v", count, err)
	}
	f.now.Add(taskLifetime + 1)
	for {
		work, err := f.service.Retain(context.Background(), f.now.Load(), 100, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !work.More {
			break
		}
	}
	if err := f.database.QueryRow("SELECT count(*) FROM request_error_bodies WHERE task_id=?", task.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("raw retention %d %v", count, err)
	}
	if _, err := f.service.readTask(context.Background(), task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("task retention %v", err)
	}
	f.checkLedger(t)
}
