package rps

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/game"
)

func queueCandidate(id string, created int64, device, ip byte) queueRecord {
	record := queueRecord{ID: id, CreatedAt: created}
	for index := range record.DeviceHash {
		record.DeviceHash[index] = device
		record.IPHash[index] = ip
	}
	return record
}

func TestSelectMatchDeviceHardBoundaryIPPreferenceAndFairness(t *testing.T) {
	tests := []struct {
		name       string
		candidates []queueRecord
		want       []string
		found      bool
	}{
		{
			name: "distinct IP preferred",
			candidates: []queueRecord{
				queueCandidate("oldest", 1, 1, 1), queueCandidate("same-ip", 2, 2, 1),
				queueCandidate("third-ip", 3, 3, 3), queueCandidate("second-ip", 4, 4, 2),
			},
			want:  []string{"oldest", "third-ip", "second-ip"},
			found: true,
		},
		{
			name: "IP fallback keeps oldest viable",
			candidates: []queueRecord{
				queueCandidate("oldest", 1, 1, 1), queueCandidate("second", 2, 2, 1), queueCandidate("third", 3, 3, 2),
			},
			want:  []string{"oldest", "second", "third"},
			found: true,
		},
		{
			name: "device collision waits for safe candidate",
			candidates: []queueRecord{
				queueCandidate("oldest", 1, 1, 1), queueCandidate("collision", 2, 1, 2),
				queueCandidate("safe-a", 3, 2, 3), queueCandidate("safe-b", 4, 3, 4),
			},
			want:  []string{"oldest", "safe-a", "safe-b"},
			found: true,
		},
		{
			name: "no device-safe triple",
			candidates: []queueRecord{
				queueCandidate("a", 1, 1, 1), queueCandidate("b", 2, 1, 2), queueCandidate("c", 3, 2, 3),
			},
			found: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, found := selectMatch(test.candidates)
			if found != test.found {
				t.Fatalf("found=%v want=%v", found, test.found)
			}
			if !found {
				return
			}
			ids := []string{got[0].ID, got[1].ID, got[2].ID}
			if !reflect.DeepEqual(ids, test.want) || !distinctDevices(got) {
				t.Fatalf("selected=%v want=%v", ids, test.want)
			}
		})
	}
}

func TestSelectMatchUsesFullQueueCapacityWindow(t *testing.T) {
	candidates := make([]queueRecord, 0, 98)
	for index := 0; index < 96; index++ {
		candidates = append(candidates, queueCandidate("same-"+string(rune(0x100+index)), int64(index), 1, 1))
	}
	candidates = append(candidates, queueCandidate("device-b", 96, 2, 1), queueCandidate("device-c", 97, 3, 1))
	selected, found := selectMatch(candidates)
	if !found || selected[0].ID != candidates[0].ID || selected[1].ID != "device-b" || selected[2].ID != "device-c" {
		t.Fatalf("full-window selection found=%v ids=%q/%q/%q", found, selected[0].ID, selected[1].ID, selected[2].ID)
	}
	if matchCandidateLimit != game.RPSQueueCapacity {
		t.Fatalf("candidate limit=%d capacity=%d", matchCandidateLimit, game.RPSQueueCapacity)
	}
}

func referenceSelectMatch(candidates []queueRecord) ([3]queueRecord, bool) {
	var fallback [3]queueRecord
	for first := 0; first+2 < len(candidates); first++ {
		for second := first + 1; second+1 < len(candidates); second++ {
			for third := second + 1; third < len(candidates); third++ {
				candidate := [3]queueRecord{candidates[first], candidates[second], candidates[third]}
				if !distinctDevices(candidate) {
					continue
				}
				if distinctIPs(candidate) {
					return candidate, true
				}
				if fallback[0].ID == "" {
					fallback = candidate
				}
			}
		}
		if fallback[0].ID != "" {
			return fallback, true
		}
	}
	return [3]queueRecord{}, false
}

func TestSelectMatchMatchesReference(t *testing.T) {
	for seed := uint32(1); seed <= 500; seed++ {
		value := seed
		next := func(modulo uint32) byte {
			value = value*1664525 + 1013904223
			return byte(value % modulo)
		}
		count := int(next(28)) + 3
		candidates := make([]queueRecord, 0, count)
		for index := 0; index < count; index++ {
			candidates = append(candidates, queueCandidate(string(rune(0x400+index)), int64(index), next(7), next(6)))
		}
		got, found := selectMatch(candidates)
		want, wantFound := referenceSelectMatch(candidates)
		if found != wantFound || found && (got[0].ID != want[0].ID || got[1].ID != want[1].ID || got[2].ID != want[2].ID) {
			t.Fatalf("seed=%d found=%v/%v got=%q/%q/%q want=%q/%q/%q", seed, found, wantFound,
				got[0].ID, got[1].ID, got[2].ID, want[0].ID, want[1].ID, want[2].ID)
		}
	}
}

func TestQueueIdempotencyAuthorizationCancelAndTimeoutRelease(t *testing.T) {
	fixture := newRPSFixture(t)
	userID, _ := fixture.seedUser("queue-contract", rpsTestFunding)
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = 0x5a
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	input := EnqueueInput{UserID: userID, Mode: game.RPSModeQuick, DeviceToken: token,
		CanonicalSourceIP: [16]byte{15: 1}, IdempotencyKey: fixture.key(500)}
	first, err := fixture.service.Enqueue(context.Background(), input)
	if err != nil || first.HTTPStatus != 202 || first.IdempotentReplay {
		t.Fatalf("first enqueue=(%+v,%v)", first, err)
	}
	replay, err := fixture.service.Enqueue(context.Background(), input)
	if err != nil || !replay.IdempotentReplay || replay.Queue != first.Queue {
		t.Fatalf("enqueue replay=(%+v,%v) first=%+v", replay, err, first)
	}
	changed := input
	changed.Mode = game.RPSModeStandard
	if _, err := fixture.service.Enqueue(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same key changed body=%v", err)
	}
	var persisted []byte
	if err := fixture.database.QueryRow(`SELECT device_token_hash FROM game_rps_queue WHERE id=?`, first.Queue.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 32 || string(persisted) == string(raw) || string(persisted) == token {
		t.Fatalf("raw device signal persisted: len=%d", len(persisted))
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Enqueue(context.Background(), input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked enqueue replay=%v", err)
	}
	if _, err := fixture.database.Exec(`UPDATE users SET is_banned=0,banned_until=NULL WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
	cancel := CancelInput{UserID: userID, QueueID: first.Queue.ID, ExpectedRevision: first.Queue.Revision, IdempotencyKey: fixture.key(501)}
	canceled, err := fixture.service.Cancel(context.Background(), cancel)
	if err != nil || canceled.HTTPStatus != 204 || canceled.IdempotentReplay {
		t.Fatalf("cancel=(%+v,%v)", canceled, err)
	}
	cancelReplay, err := fixture.service.Cancel(context.Background(), cancel)
	if err != nil || !cancelReplay.IdempotentReplay {
		t.Fatalf("cancel replay=(%+v,%v)", cancelReplay, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_user_slots WHERE user_id=?`, userID) != 0 ||
		fixture.balance(userID).Cmp(big.NewInt(rpsTestFunding)) != 0 {
		t.Fatal("cancel did not atomically release queue slot and balance")
	}

	timed := fixture.enqueue(userID, game.RPSModeQuick, 0x6a, 2, 502)
	fixture.clock.Store(timed.Queue.Deadline)
	count, err := fixture.service.sweepQueues(context.Background(), fixture.clock.Load())
	if err != nil || count != 1 {
		t.Fatalf("queue sweep=(%d,%v)", count, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue WHERE id=?`, timed.Queue.ID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_user_slots WHERE user_id=?`, userID) != 0 ||
		fixture.balance(userID).Cmp(big.NewInt(rpsTestFunding)) != 0 {
		t.Fatal("timeout did not atomically release queue slot and balance")
	}
}

func TestDeviceTokenCanonicalBoundary(t *testing.T) {
	key := [32]byte{0: 1}
	raw := make([]byte, 32)
	for index := range raw {
		raw[index] = byte(index)
	}
	canonical := base64.RawURLEncoding.EncodeToString(raw)
	first, err := hashDeviceToken(key, canonical)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashDeviceToken(key, canonical)
	if err != nil || first != second {
		t.Fatalf("stable device HMAC=(%x,%x,%v)", first, second, err)
	}
	for _, invalid := range []string{"", canonical + "=", canonical[:42], strings.Repeat("A", 42) + "*"} {
		if _, err := hashDeviceToken(key, invalid); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid token accepted %q: %v", invalid, err)
		}
	}
}

func TestMatchCapacityFailureKeepsAllQueuesReserved(t *testing.T) {
	fixture := newRPSFixture(t)
	users := make([]int64, 3)
	queues := make([]QueueMutationResult, 3)
	for index := range users {
		users[index], _ = fixture.seedUser("capacity-"+string(rune('a'+index)), rpsTestFunding)
		queues[index] = fixture.enqueue(users[index], game.RPSModeQuick, byte(0x70+index), byte(0x70+index), 1200+index)
	}
	future, err := sessionFutureRows(fixture.clock.Load())
	if err != nil || !future.Big().IsInt64() {
		t.Fatalf("future rows=%s err=%v", future.Decimal(), err)
	}
	if _, err := fixture.database.Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, math.MaxInt64-future.Big().Int64()); err != nil {
		t.Fatal(err)
	}
	matched, err := fixture.service.MatchOnce(context.Background(), game.RPSModeQuick)
	if matched || !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("capacity match=(%v,%v)", matched, err)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue WHERE mode='quick'`) != 3 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_user_slots WHERE queue_id IS NOT NULL`) != 3 ||
		fixture.scalar(`SELECT COUNT(*) FROM game_rps_sessions`) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='rps_session_start'`) != 0 {
		t.Fatal("capacity failure partially started or released queues")
	}
	for index, userID := range users {
		if got := fixture.balance(userID); got.Cmp(big.NewInt(rpsTestFunding-1000)) != 0 {
			t.Fatalf("user %d balance=%s", userID, got)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue WHERE id=?`, queues[index].Queue.ID) != 1 {
			t.Fatalf("queue %s missing", queues[index].Queue.ID)
		}
	}
	var reservedRaw []byte
	if err := fixture.database.QueryRow(`SELECT reserved_future_rows FROM credit_capacity WHERE id=1`).Scan(&reservedRaw); err != nil {
		t.Fatal(err)
	}
	reserved, err := db.DecodeU128(reservedRaw)
	if err != nil || reserved.Decimal() != "3" {
		t.Fatalf("reserved future rows=%s err=%v", reserved.Decimal(), err)
	}
}

func TestMatchRevalidatesCurrentBaseAndReleasesStaleQueues(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		initialBase int64
		currentBase int64
	}{
		{name: "quick increased", mode: game.RPSModeQuick, initialBase: 1_000, currentBase: 2_000},
		{name: "quick decreased", mode: game.RPSModeQuick, initialBase: 2_000, currentBase: 1_000},
		{name: "standard changed", mode: game.RPSModeStandard, initialBase: 1_000, currentBase: 2_000},
		{name: "deathmatch below new minimum", mode: game.RPSModeDeathmatch, initialBase: 1_000, currentBase: 200_000},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRPSFixture(t)
			fixture.setRPSConfig(true, test.initialBase, PumpsBP{Platform: 100, Welfare: 100, Thursday: 100})
			users := make([]int64, 0, 3)
			for seat := 0; seat < 3; seat++ {
				userID, _ := fixture.seedUser(test.name+strconv.Itoa(seat), rpsTestFunding)
				users = append(users, userID)
				fixture.enqueue(userID, test.mode, byte(index*8+seat+1), byte(seat+1), 7_000+index*10+seat)
			}
			fixture.setRPSConfig(true, test.currentBase, PumpsBP{Platform: 100, Welfare: 100, Thursday: 100})
			matched, err := fixture.service.MatchOnce(context.Background(), test.mode)
			if err != nil || matched {
				t.Fatalf("stale match=(%v,%v)", matched, err)
			}
			if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_queue`); got != 0 {
				t.Fatalf("stale queues=%d", got)
			}
			if got := fixture.scalar(`SELECT COUNT(*) FROM game_rps_user_slots`); got != 0 {
				t.Fatalf("stale slots=%d", got)
			}
			for _, userID := range users {
				if got := fixture.balance(userID); got.Cmp(big.NewInt(rpsTestFunding)) != 0 {
					t.Fatalf("user %d balance=%s", userID, got)
				}
			}
		})
	}
}
