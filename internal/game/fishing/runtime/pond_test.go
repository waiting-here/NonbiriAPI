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
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func pondPublic(t *testing.T, f *gameFixture, userID int64, public bool) {
	t.Helper()
	if _, err := f.database.Exec(`UPDATE users SET game_profile_public=? WHERE id=?`, boolInteger(public), userID); err != nil {
		t.Fatal(err)
	}
}

func pondCatch(t *testing.T, f *gameFixture, userID int64, key int, species ...uint64) *FishingBatchResult {
	t.Helper()
	f.random.mu.Lock()
	f.random.values = nil
	for _, value := range species {
		f.random.values = append(f.random.values, 0, value)
	}
	f.random.mu.Unlock()
	result, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: userID, Bait: "worm", Count: len(species), IdempotencyKey: validTestKey(key)})
	if err != nil || pending != nil || result == nil {
		t.Fatalf("pond catch: result=%#v pending=%#v err=%v", result, pending, err)
	}
	return result
}

func pondReturn(t *testing.T, f *gameFixture, userID int64, batch *FishingBatchResult, ordinal int) PondReturnReceipt {
	t.Helper()
	receipt, err := f.service.ReturnPondOutcome(context.Background(), userID, batch.BatchID, ordinal)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestPondReturnAuthorityFryAndNaturalReplay(t *testing.T) {
	f := newGameFixture(t, nil)
	f.service.pondRandom = &scriptedSource{max: true}
	owner := f.seedUser("pond-owner", fixtureFunding)
	other := f.seedUser("pond-other", fixtureFunding)
	catch := pondCatch(t, f, owner, 900, 0)
	if catch.Outcomes[0].Pond == nil || catch.Outcomes[0].Pond.ReturnState != "available" {
		t.Fatal(catch.Outcomes)
	}
	if _, err := f.service.ReturnPondOutcome(context.Background(), owner, catch.BatchID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous return: %v", err)
	}
	if _, err := f.service.ReturnPondOutcome(context.Background(), other, catch.BatchID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign return: %v", err)
	}
	pondPublic(t, f, owner, true)
	receipt := pondReturn(t, f, owner, catch, 0)
	pondPublic(t, f, owner, false)
	again := pondReturn(t, f, owner, catch, 0)
	if receipt != again || f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='gifts'`, owner) != 1 {
		t.Fatalf("repeat changed authority: %#v %#v", receipt, again)
	}
	fry := pondCatch(t, f, owner, 901, 7)
	if _, err := f.service.ReturnPondOutcome(context.Background(), owner, fry.BatchID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous fry release: %v", err)
	}
	pondPublic(t, f, owner, true)
	released := pondReturn(t, f, owner, fry, 0)
	if released.ReturnState != "released" || f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, owner) != 1 {
		t.Fatal("fry entered queue/count")
	}
	legacy, pending, err := f.service.startLegacy(context.Background(), StartInput{UserID: owner, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(902)})
	if err != nil || pending != nil || legacy == nil || legacy.Outcomes[0].Pond != nil {
		t.Fatalf("legacy: %#v %v", legacy, err)
	}
	if _, err = f.service.ReturnPondOutcome(context.Background(), owner, legacy.BatchID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy return: %v", err)
	}
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, waiting, err := f.service.StartFishing(context.Background(), StartInput{UserID: other, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(903)})
	if err != nil || waiting == nil {
		t.Fatal(err)
	}
	if _, err = f.service.ReturnPondOutcome(context.Background(), other, waiting.BatchID, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending return: %v", err)
	}
	f.service.beforeSettlement = nil
	f.userAuth.setError(ErrForbidden)
	if _, err = f.service.ReturnPondOutcome(context.Background(), owner, catch.BatchID, 0); err == nil {
		t.Fatal("receipt bypassed current account authority")
	}
}

func TestPondFIFOIndependentBaseDrawsAndReplay(t *testing.T) {
	f := newGameFixture(t, nil)
	f.service.pondRandom = &scriptedSource{max: true}
	donor := f.seedUser("pond-signed", fixtureFunding)
	receiver := f.seedUser("pond-receiver", fixtureFunding)
	pondPublic(t, f, donor, true)
	donation := pondCatch(t, f, donor, 910, 0, 1, 2, 3, 4, 5, 6, 7, 0, 1)
	for ordinal := 0; ordinal < 3; ordinal++ {
		pondReturn(t, f, donor, donation, ordinal)
	}
	pondPublic(t, f, donor, false)
	random := &scriptedSource{}
	f.service.pondRandom = random
	before := f.random.callCount()
	catch := pondCatch(t, f, receiver, 911, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7)
	if f.random.callCount()-before != 20 || random.callCount() != 3 {
		t.Fatalf("random calls: base=%d pond=%d", f.random.callCount()-before, random.callCount())
	}
	for ordinal, want := range []string{"boot", "seaweed", "plastic_bag"} {
		outcome := catch.Outcomes[ordinal]
		if outcome.SpeciesKey != want || outcome.Pond.ReturnState != "gift" || outcome.Pond.Gift.Kind != "public" || outcome.Pond.Gift.DisplayName != "pond-signed" || outcome.Reward != "0" || outcome.NetReward != "0" {
			t.Fatalf("gift %d: %#v", ordinal, outcome)
		}
		if _, err := f.service.ReturnPondOutcome(context.Background(), receiver, catch.BatchID, ordinal); !errors.Is(err, ErrConflict) {
			t.Fatalf("gift returned: %v", err)
		}
	}
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_outcomes WHERE batch_id=? AND species_key='fry'`, catch.BatchID) != 10 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, receiver) != 3 || f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 0 {
		t.Fatal("base changed, actual gift not counted, or queue not consumed")
	}
	replay, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: receiver, Bait: "worm", Count: 10, IdempotencyKey: validTestKey(911)})
	if err != nil || pending != nil || !replay.IdempotentReplay || !reflect.DeepEqual(replay.Outcomes, catch.Outcomes) || random.callCount() != 3 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	board, err := f.service.PondLeaderboard(context.Background(), donor, "gifts", "")
	if err != nil || board.Period != "7d" || len(board.Entries) != 1 || board.Entries[0].Identity.Kind != "anonymous" {
		t.Fatalf("board=%#v err=%v", board, err)
	}
	if _, err = f.database.Exec(`UPDATE users SET username='renamed-donor' WHERE id=?`, donor); err != nil {
		t.Fatal(err)
	}
	state, err := f.service.FishingState(context.Background(), receiver)
	if err != nil || state.Unrevealed.Outcomes[0].Pond.Gift.DisplayName != "pond-signed" {
		t.Fatal("snapshot followed rename or anonymity")
	}
}

func TestPondSettlementRollbackDoesNotLoseQueueOrCount(t *testing.T) {
	f := newGameFixture(t, nil)
	f.service.pondRandom = &scriptedSource{max: true}
	donor := f.seedUser("rollback-donor", fixtureFunding)
	receiver := f.seedUser("rollback-recipient", fixtureFunding)
	pondPublic(t, f, receiver, true)
	own := pondCatch(t, f, receiver, 919, 0)
	pondReturn(t, f, receiver, own, 0)
	pondPublic(t, f, donor, true)
	donation := pondCatch(t, f, donor, 920, 1)
	pondReturn(t, f, donor, donation, 0)
	f.service.pondRandom = &scriptedSource{}
	if _, err := f.database.Exec(`CREATE TRIGGER pond_fail_terminal BEFORE UPDATE OF state ON game_fishing_batches WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: receiver, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(921)})
	if err != nil || pending == nil {
		t.Fatalf("rollback: %#v %v", pending, err)
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 2 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_outcomes WHERE batch_id=? AND gift_species_key IS NOT NULL`, pending.BatchID) != 0 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, receiver) != 1 {
		t.Fatal("rollback lost gift or committed counts")
	}
	assertPondHeadsMatchQueue(t, f)
	if _, err = f.database.Exec(`DROP TRIGGER pond_fail_terminal`); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.settle(context.Background(), pending.BatchID, receiver, f.clock.Load(), false)
	if err != nil || result.Outcomes[0].SpeciesKey != "seaweed" || f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_queue WHERE donor_user_id=?`, receiver) != 1 {
		t.Fatalf("recovery=%#v %v", result, err)
	}
	assertPondHeadsMatchQueue(t, f)
}

func TestPondConcurrentReturnsAndConsumptionExactlyOnce(t *testing.T) {
	f := newGameFixture(t, nil)
	donor := f.seedUser("concurrent-donor", fixtureFunding)
	pondPublic(t, f, donor, true)
	donation := pondCatch(t, f, donor, 930, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errorsOut := make(chan error, 12)
	for index := 0; index < 12; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 50; attempt++ {
				_, err := f.service.ReturnPondOutcome(context.Background(), donor, donation.BatchID, 0)
				if errors.Is(err, ErrServiceUnavailable) {
					time.Sleep(time.Millisecond)
					continue
				}
				errorsOut <- err
				return
			}
			errorsOut <- errors.New("return retry exhausted")
		}()
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='gifts'`, donor) != 1 {
		t.Fatal("duplicate queue/count")
	}
	f.service.beforeSettlement = func(string) error { return errInjected }
	users := []int64{f.seedUser("consumer-one", fixtureFunding), f.seedUser("consumer-two", fixtureFunding)}
	ids := make([]string, 0, 2)
	for index, user := range users {
		_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(931 + index)})
		if err != nil || pending == nil {
			t.Fatal(err)
		}
		ids = append(ids, pending.BatchID)
	}
	f.service.beforeSettlement = nil
	f.service.pondRandom = &scriptedSource{}
	start = make(chan struct{})
	errorsOut = make(chan error, 2)
	for index := range users {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			for attempt := 0; attempt < 50; attempt++ {
				_, err := f.service.settle(context.Background(), ids[index], users[index], f.clock.Load(), false)
				if errors.Is(err, ErrServiceUnavailable) {
					time.Sleep(time.Millisecond)
					continue
				}
				errorsOut <- err
				return
			}
			errorsOut <- errors.New("consume retry exhausted")
		}(index)
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_outcomes WHERE gift_species_key IS NOT NULL`) != 1 || f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 0 {
		t.Fatal("gift consumed twice")
	}
}

func TestPondExportDeletionRetentionAndWindowBoundaries(t *testing.T) {
	f := newGameFixture(t, nil)
	f.service.pondRandom = &scriptedSource{max: true}
	donor := f.seedUser("private-donor-label", fixtureFunding)
	receiver := f.seedUser("export-receiver", fixtureFunding)
	pondPublic(t, f, donor, true)
	donation := pondCatch(t, f, donor, 940, 0, 1, 2, 3, 4, 5, 6, 7, 0, 1)
	pondReturn(t, f, donor, donation, 0)
	pondReturn(t, f, donor, donation, 1)
	f.service.pondRandom = &scriptedSource{}
	catch := pondCatch(t, f, receiver, 941, 7)
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := f.service.Lifecycle().ExportUser(context.Background(), tx, receiver, f.clock.Load())
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(exported)
	if strings.Contains(string(body), "private-donor-label") || strings.Contains(string(body), "donor_user_id") || exported.Terminal[0].Outcomes[0].Pond.Gift.Kind != "anonymous" {
		t.Fatalf("unsafe export: %s", body)
	}
	tx, err = f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ownerExport, err := f.service.Lifecycle().ExportUser(context.Background(), tx, donor, f.clock.Load())
	tx.Rollback()
	if err != nil || len(ownerExport.PondQueue) != 1 || ownerExport.PondQueue[0].SpeciesKey != "seaweed" || ownerExport.PondQueue[0].Identity.DisplayName != "private-donor-label" {
		t.Fatalf("own queue export=%#v %v", ownerExport.PondQueue, err)
	}
	for _, test := range []struct {
		offset       int64
		period, want string
	}{{7*86400 - 1, "7d", "1"}, {7 * 86400, "7d", "0"}, {30*86400 - 1, "30d", "1"}, {30 * 86400, "30d", "0"}, {31 * 86400, "all", "1"}} {
		f.clock.Store(fixtureNow + test.offset)
		board, err := f.service.PondLeaderboard(context.Background(), receiver, "cleanup", test.period)
		if err != nil {
			t.Fatal(err)
		}
		got := "0"
		for _, entry := range board.Entries {
			if entry.IsMe {
				got = entry.ItemCount
			}
		}
		if got != test.want {
			t.Fatalf("offset%d period%s count=%s", test.offset, test.period, got)
		}
	}
	// Delete before retention to inspect another user's retained gift snapshot.
	tx, err = f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.Lifecycle().PrepareDeleteTx(context.Background(), tx, donor, f.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err := f.service.FishingState(context.Background(), receiver)
	if err != nil || state.Unrevealed.Outcomes[0].Pond.Gift.Kind != "anonymous" || state.Unrevealed.Outcomes[0].SpeciesKey != "boot" {
		t.Fatalf("deleted gift=%#v err=%v", state, err)
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 0 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_counts WHERE user_id=?`, donor) != 0 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_totals WHERE user_id=?`, donor) != 0 {
		t.Fatal("donor deletion left data")
	}
	for pass := 0; pass < 10; pass++ {
		retained, err := f.service.Lifecycle().Retain(context.Background(), f.clock.Load(), 1, time.Now().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if !retained.More {
			break
		}
	}
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, catch.BatchID) != 0 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_counts`) != 0 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, receiver) != 1 {
		t.Fatal("retention lost lifetime or retained details")
	}
}

func TestPondQueueOutlivesCatchAndHTTPRejectsForgery(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("long-lived-queue", fixtureFunding)
	pondPublic(t, f, user, true)
	catch := pondCatch(t, f, user, 950, 4)
	api := &httpAPI{service: f.service}
	for _, body := range []string{`{"species_key":"boot"}`, `{"donor_user_id":1}`} {
		request := httptest.NewRequest(http.MethodPost, "http://example.test/return", strings.NewReader(body))
		request.SetPathValue("id", catch.BatchID)
		request.SetPathValue("ordinal", "0")
		response := httptest.NewRecorder()
		api.pondReturn(response, request, resources.UserPrincipal{UserID: user})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("forgery status=%d body=%s", response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.test/return", nil)
	request.SetPathValue("id", catch.BatchID)
	request.SetPathValue("ordinal", "0")
	response := httptest.NewRecorder()
	api.pondReturn(response, request, resources.UserPrincipal{UserID: user})
	if response.Code != http.StatusOK {
		t.Fatalf("return status=%d body=%s", response.Code, response.Body.String())
	}
	now := fixtureNow + 31*86400
	for pass := 0; pass < 10; pass++ {
		value, err := f.service.Lifecycle().Retain(context.Background(), now, 1, time.Now().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if !value.More {
			break
		}
	}
	if f.scalar(`SELECT COUNT(*) FROM game_fishing_batches WHERE id=?`, catch.BatchID) != 0 || f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_queue WHERE species_key='old_tire'`) != 1 {
		t.Fatal("retention removed live queue")
	}
	for _, query := range []string{"board=cleanup", "board=gifts&period=all", "board=cleanup&period=30d"} {
		request = httptest.NewRequest(http.MethodGet, "http://example.test/pond?"+query, nil)
		response = httptest.NewRecorder()
		api.pondLeaderboard(response, request, resources.UserPrincipal{UserID: user})
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
	for _, query := range []string{"board=oops", "board=cleanup&period=", "board=cleanup&period=7d&period=all", "board=cleanup&user_id=1"} {
		request = httptest.NewRequest(http.MethodGet, "http://example.test/pond?"+query, nil)
		response = httptest.NewRecorder()
		api.pondLeaderboard(response, request, resources.UserPrincipal{UserID: user})
		if response.Code != http.StatusBadRequest {
			t.Fatal(fmt.Sprintf("query %s status=%d", query, response.Code))
		}
	}
}

func TestPondCurrentPreferencesAndPreUpgradePending(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("pond-preferences", fixtureFunding)
	pondPublic(t, f, user, true)
	catch := pondCatch(t, f, user, 960, 0)
	if _, err := f.database.Exec(`INSERT INTO game_user_preferences(user_id,game_profile_public,updated_at) VALUES(?,0,?)`, user, fixtureNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReturnPondOutcome(context.Background(), user, catch.BatchID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("old public field overrode current anonymous preference: %v", err)
	}
	board, err := f.service.PondLeaderboard(context.Background(), user, "cleanup", "7d")
	if err != nil || board.Entries[0].Identity.Kind != "anonymous" {
		t.Fatalf("board=%#v %v", board, err)
	}
	if _, err = f.database.Exec(`UPDATE game_user_preferences SET game_profile_public=1 WHERE user_id=?`, user); err != nil {
		t.Fatal(err)
	}
	pondReturn(t, f, user, catch, 0)
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(961)})
	if err != nil || pending == nil {
		t.Fatal(err)
	}
	// Schema44 reserved batches have the same rules version but no marker.
	if _, err = f.database.Exec(`DELETE FROM game_fishing_pond_outcomes WHERE batch_id=?`, pending.BatchID); err != nil {
		t.Fatal(err)
	}
	f.service.beforeSettlement = nil
	f.service.pondRandom = &scriptedSource{}
	result, err := f.service.settle(context.Background(), pending.BatchID, user, fixtureNow, false)
	if err != nil || result.Outcomes[0].Pond != nil {
		t.Fatalf("old pending result=%#v %v", result, err)
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT item_count FROM game_fishing_pond_totals WHERE user_id=? AND board='cleanup'`, user) != 1 {
		t.Fatal("old pending received gift or counted")
	}
}

func TestPondReturnRollbackAndDonorDeletionRace(t *testing.T) {
	f := newGameFixture(t, nil)
	f.service.pondRandom = &scriptedSource{max: true}
	donor := f.seedUser("delete-race-donor", fixtureFunding)
	receiver := f.seedUser("delete-race-recipient", fixtureFunding)
	pondPublic(t, f, receiver, true)
	own := pondCatch(t, f, receiver, 989, 0)
	pondReturn(t, f, receiver, own, 0)
	pondPublic(t, f, donor, true)
	donation := pondCatch(t, f, donor, 990, 2)
	if _, err := f.database.Exec(`CREATE TRIGGER pond_fail_return BEFORE UPDATE OF returned_at ON game_fishing_pond_outcomes WHEN NEW.returned_at IS NOT NULL BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReturnPondOutcome(context.Background(), donor, donation.BatchID, 0); err == nil {
		t.Fatal("return fault ignored")
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_totals WHERE user_id=? AND board='gifts'`, donor) != 0 {
		t.Fatal("failed return left queue/count")
	}
	if _, err := f.database.Exec(`DROP TRIGGER pond_fail_return`); err != nil {
		t.Fatal(err)
	}
	pondReturn(t, f, donor, donation, 0)
	f.service.beforeSettlement = func(string) error { return errInjected }
	_, pending, err := f.service.StartFishing(context.Background(), StartInput{UserID: receiver, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(991)})
	if err != nil || pending == nil {
		t.Fatal(err)
	}
	f.service.beforeSettlement = nil
	f.service.pondRandom = &scriptedSource{}
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, err := f.service.settle(context.Background(), pending.BatchID, receiver, fixtureNow, false)
		done <- err
	}()
	go func() {
		<-start
		tx, err := f.database.BeginTx(context.Background(), nil)
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback()
		if err = f.service.Lifecycle().PrepareDeleteTx(context.Background(), tx, donor, fixtureNow); err != nil {
			done <- err
			return
		}
		_, err = tx.Exec(`UPDATE users SET is_banned=1 WHERE id=?`, donor)
		if err != nil {
			done <- err
			return
		}
		done <- tx.Commit()
	}()
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	state, err := f.service.FishingState(context.Background(), receiver)
	if err != nil || state.Unrevealed == nil {
		t.Fatal(err)
	}
	pond := state.Unrevealed.Outcomes[0].Pond
	if pond.Gift != nil && pond.Gift.Kind != "anonymous" {
		t.Fatal("donor race left public snapshot")
	}
	if f.scalar(`SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1`) != 1 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_queue WHERE donor_user_id=?`, receiver) != 1 || f.scalar(`SELECT COUNT(*) FROM game_fishing_pond_outcomes WHERE donor_user_id=?`, donor) != 0 {
		t.Fatal("donor race left identity or queue")
	}
	assertPondHeadsMatchQueue(t, f)
}

func TestPondDailyAggregationMatchesExactWindowsAndExport(t *testing.T) {
	f := newGameFixture(t, nil)
	user := f.seedUser("bucket-owner", fixtureFunding)
	other := f.seedUser("bucket-other", fixtureFunding)
	day := pondDayStart(fixtureNow)
	for _, offset := range []int64{0, 12345, 86399} {
		now := day + offset
		if _, err := f.database.Exec(`DELETE FROM game_fishing_pond_counts;DELETE FROM game_fishing_pond_daily;DELETE FROM game_fishing_pond_totals`); err != nil {
			t.Fatal(err)
		}
		tx, err := f.database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		times := []int64{now - 30*86400 - 1, now - 30*86400, now - 30*86400 + 1, pondDayStart(now-30*86400) + 86399, now - 20*86400, now - 7*86400 - 1, now - 7*86400, now - 7*86400 + 1, pondDayStart(now) - 1, pondDayStart(now), now, now + 1, now + 86400}
		for index, at := range times {
			for _, board := range []string{"cleanup", "gifts"} {
				if err = f.service.addPondCountTx(context.Background(), tx, user, board, at, int64(index+1)); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = f.service.addPondCountTx(context.Background(), tx, other, board, at, 1); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		f.clock.Store(now)
		for _, period := range []string{"7d", "30d", "all"} {
			window, _ := pondWindow(period, now)
			for _, board := range []string{"cleanup", "gifts"} {
				var want int64
				if window == nil {
					want = f.scalar(`SELECT SUM(item_count) FROM game_fishing_pond_counts WHERE user_id=? AND board=?`, user, board)
				} else {
					want = f.scalar(`SELECT SUM(item_count) FROM game_fishing_pond_counts WHERE user_id=? AND board=? AND occurred_at>? AND occurred_at<=?`, user, board, *window, now)
				}
				got, err := f.service.PondLeaderboard(context.Background(), user, board, period)
				if err != nil || len(got.Entries) != 2 || !got.Entries[0].IsMe || got.Entries[0].ItemCount != fmt.Sprint(want) {
					t.Fatalf("offset=%d %s/%s board=%#v want=%d err=%v", offset, board, period, got, want, err)
				}
				tx, err = f.database.BeginTx(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				exported, err := f.service.Lifecycle().ExportUser(context.Background(), tx, user, now)
				tx.Rollback()
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, stat := range exported.PondStats {
					if stat.Board == board && stat.Period == period {
						found = true
						if stat.ItemCount != fmt.Sprint(want) {
							t.Fatalf("export=%#v want=%d", stat, want)
						}
					}
				}
				if !found {
					t.Fatal("missing export score")
				}
			}
		}
		// Equal counts use the actual latest contributing fact, not midnight.
		cutoff := now - 7*86400
		scores, args := pondScoresSQL("cleanup", &cutoff, now, user)
		var gotAt int64
		if err = f.store.ReadDB().QueryRow(`SELECT achieved_at FROM (`+scores+`)`, args...).Scan(&gotAt); err != nil || gotAt != now {
			t.Fatalf("latest contributing time=%d want=%d err=%v", gotAt, now, err)
		}
	}
}
