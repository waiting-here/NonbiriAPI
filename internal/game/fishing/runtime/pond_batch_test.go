package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func TestPondBatchReturnMixedAuthorityAndCrossReplay(t *testing.T) {
	f := newGameFixture(t, nil)
	owner := f.seedUser("batch-owner", fixtureFunding)
	other := f.seedUser("batch-other", fixtureFunding)
	donor := f.seedUser("batch-donor", fixtureFunding)
	pondPublic(t, f, donor, true)
	donation := pondCatch(t, f, donor, 980, 0)
	pondReturn(t, f, donor, donation, 0)
	f.service.pondRandom = &scriptedSource{}
	// Nine ordinary junk draws followed by a real valuable treasure draw.
	f.random.mu.Lock()
	f.random.values = []uint64{0, 0, 0, 7, 0, 1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0, 0}
	f.random.max = true
	f.random.mu.Unlock()
	batch, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: owner, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(981)})
	if err != nil || pending != nil || batch == nil {
		t.Fatalf("mixed catch=%#v pending=%#v err=%v", batch, pending, err)
	}
	if batch.Outcomes[0].Pond.ReturnState != "gift" || batch.Outcomes[1].SpeciesKey != "fry" || batch.Outcomes[9].Tier == "junk" || batch.Outcomes[9].Reward == "0" {
		t.Fatalf("mixed fixture=%#v", batch.Outcomes)
	}
	for _, test := range []struct {
		user int64
		want error
	}{{other, ErrNotFound}, {owner, ErrForbidden}} {
		if _, err = f.service.ReturnPondBatch(context.Background(), test.user, batch.BatchID); !errors.Is(err, test.want) {
			t.Fatalf("authority user=%d: %v", test.user, err)
		}
	}
	pondPublic(t, f, owner, true)
	first := pondReturn(t, f, owner, batch, 2)
	f.clock.Add(1)
	before := f.userAuth.calls.Load()
	result, err := f.service.ReturnPondBatch(context.Background(), owner, batch.BatchID)
	if err != nil || len(result.Receipts) != 8 || f.userAuth.calls.Load()-before != 1 {
		t.Fatalf("batch=%#v authCalls=%d err=%v", result, f.userAuth.calls.Load()-before, err)
	}
	for index, receipt := range result.Receipts {
		if receipt.Ordinal != index+1 || receipt.BatchID != batch.BatchID {
			t.Fatalf("receipt order=%#v", result.Receipts)
		}
		if receipt.Ordinal == 1 && receipt.ReturnState != "released" {
			t.Fatal("fry did not release")
		}
		if receipt.Ordinal == first.Ordinal && receipt != first {
			t.Fatal("batch changed previous single receipt")
		}
		if receipt.Ordinal != first.Ordinal && receipt.ReturnedAt != fixtureNow+1 {
			t.Fatal("new batch receipt time changed")
		}
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 7 ||
		f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='gifts'`, owner) != 7 ||
		f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_queue WHERE donor_user_id=? AND donor_display_name='batch-owner'`, owner) != 7 ||
		f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_outcomes WHERE batch_id=? AND ordinal IN (0,9) AND returned_at IS NOT NULL`, batch.BatchID) != 0 {
		t.Fatal("mixed batch changed gift, valuable catch, signature or counts")
	}
	pondPublic(t, f, owner, false)
	again, err := f.service.ReturnPondBatch(context.Background(), owner, batch.BatchID)
	if err != nil || !reflect.DeepEqual(result, again) {
		t.Fatalf("anonymous replay=%#v err=%v", again, err)
	}
	for _, receipt := range result.Receipts {
		if got := pondReturn(t, f, owner, batch, receipt.Ordinal); got != receipt {
			t.Fatalf("single replay changed batch receipt: %#v %#v", got, receipt)
		}
	}
	if _, err = f.service.ReturnPondOutcome(context.Background(), owner, batch.BatchID, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("gift return=%v", err)
	}
	if _, err = f.service.ReturnPondOutcome(context.Background(), owner, batch.BatchID, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("valuable return=%v", err)
	}
	f.userAuth.setError(ErrForbidden)
	if _, err = f.service.ReturnPondBatch(context.Background(), owner, batch.BatchID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("replay bypassed current account: %v", err)
	}
}

func TestPondBatchReturnFryLegacyAndPending(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("batch-fry", fixtureFunding)
	pondPublic(t, f, user, true)
	batch := pondCatch(t, f, user, 982, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7)
	result, err := f.service.ReturnPondBatch(context.Background(), user, batch.BatchID)
	if err != nil || len(result.Receipts) != 10 {
		t.Fatalf("fry result=%#v err=%v", result, err)
	}
	for _, receipt := range result.Receipts {
		if receipt.ReturnState != "released" {
			t.Fatal(receipt)
		}
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 0 ||
		f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_totals WHERE user_id=?`, user) != 0 {
		t.Fatal("fry changed pond queue or either count")
	}
	legacy, pending, err := f.service.startLegacy(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(983)})
	if err != nil || pending != nil || legacy == nil {
		t.Fatalf("legacy=%#v pending=%#v err=%v", legacy, pending, err)
	}
	result, err = f.service.ReturnPondBatch(context.Background(), user, legacy.BatchID)
	if err != nil || len(result.Receipts) != 0 || result.Receipts == nil {
		t.Fatalf("legacy gained eligibility: %#v %v", result, err)
	}
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err = f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(984)})
	if err != nil || pending == nil {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
	if _, err = f.service.ReturnPondBatch(context.Background(), user, pending.BatchID); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending batch return: %v", err)
	}
}

func TestPondBatchReturnRollbackAndConcurrentSingles(t *testing.T) {
	f := newGameFixture(t, nil)
	f.service.pondRandom = &scriptedSource{max: true}
	user := f.seedUser("atomic-batch", fixtureFunding)
	pondPublic(t, f, user, true)
	batch := pondCatch(t, f, user, 985, 0, 1, 2, 3, 4, 5, 6, 7, 0, 1)
	trigger := fmt.Sprintf(`CREATE TRIGGER fail_pond_batch BEFORE UPDATE OF returned_at ON game_fishing_pond_outcomes
WHEN NEW.batch_id='%s' AND NEW.ordinal=5 BEGIN SELECT RAISE(ABORT,'injected'); END`, batch.BatchID)
	if _, err := f.database.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReturnPondBatch(context.Background(), user, batch.BatchID); err == nil {
		t.Fatal("injected partial batch succeeded")
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 0 ||
		f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_outcomes WHERE batch_id=? AND returned_at IS NOT NULL`, batch.BatchID) != 0 ||
		f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_totals WHERE user_id=? AND board='gifts'`, user) != 0 {
		t.Fatal("failed batch left partial queue, receipts or counts")
	}
	if _, err := f.database.Exec("DROP TRIGGER fail_pond_batch"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 8)
	var wg sync.WaitGroup
	for index := range 8 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			if index%2 == 0 {
				_, err := f.service.ReturnPondBatch(context.Background(), user, batch.BatchID)
				done <- err
			} else {
				_, err := f.service.ReturnPondOutcome(context.Background(), user, batch.BatchID, index)
				done <- err
			}
		}(index)
	}
	close(start)
	wg.Wait()
	close(done)
	for err := range done {
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.service.ReturnPondBatch(context.Background(), user, batch.BatchID)
	if err != nil || len(result.Receipts) != 10 {
		t.Fatalf("retry=%#v err=%v", result, err)
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 9 ||
		f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='gifts'`, user) != 9 ||
		f.scalar(`SELECT SUM(item_count) FROM game_fishing_pond_counts WHERE user_id=? AND board='gifts'`, user) != 9 ||
		f.scalar(`SELECT SUM(item_count) FROM game_fishing_pond_daily WHERE user_id=? AND board='gifts'`, user) != 9 {
		t.Fatal("concurrent batch/single return duplicated counts or queue")
	}
}

func TestPondBatchReturnHTTPRejectsForgeryAndReturnsReceipts(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("http-batch", fixtureFunding)
	pondPublic(t, f, user, true)
	batch := pondCatch(t, f, user, 986, 0, 1, 2, 3, 4, 5, 6, 7, 0, 1)
	api := &httpAPI{service: f.service}
	for _, test := range []struct{ query, body string }{
		{"", `{"ordinals":[0]}`}, {"", `{"donor_user_id":1}`}, {"?ordinal=0", ""},
	} {
		request := httptest.NewRequest(http.MethodPost, "http://example.test/return"+test.query, strings.NewReader(test.body))
		request.SetPathValue("id", batch.BatchID)
		response := httptest.NewRecorder()
		api.pondBatchReturn(response, request, resources.UserPrincipal{UserID: user})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("forgery status=%d body=%s", response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.test/return", nil)
	request.SetPathValue("id", batch.BatchID)
	response := httptest.NewRecorder()
	api.pondBatchReturn(response, request, resources.UserPrincipal{UserID: user})
	var result PondBatchReturnReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || result.BatchID != batch.BatchID || len(result.Receipts) != 10 {
		t.Fatalf("status=%d result=%#v err=%v", response.Code, result, err)
	}
}
