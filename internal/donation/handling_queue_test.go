package donation

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

func TestTraineeHandlingConcurrentIntentsHaveOneWinner(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	actor := e.seedUser(t, "concurrent-handler", &five, false)
	owner := e.seedUser(t, "concurrent-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'h')
	d := approvedResetDonation(t, e, owner, physical)
	id := parseTestID(t, d.ID)
	model := controlModel(t, e, "handling-mainstream", true)
	e.seedBinding(t, model, parseTestID(t, d.Keys[0].ID), "model", 0)
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, seed := range []byte{'S', 'T'} {
		mutation := donationMutation(t, seed, http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
		group.Go(func() {
			<-start
			_, err := e.service.ProcessSteward(context.Background(), actor, id, mutation, 1)
			results <- err
		})
	}
	close(start)
	group.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("concurrent results", wins, conflicts)
	}
	var business, handling int64
	if err := e.store.DB().QueryRow(`SELECT d.revision,h.revision FROM donations d JOIN donation_handling h ON h.donation_id=d.id WHERE d.id=?`, id).Scan(&business, &handling); err != nil || business != 2 || handling != 2 {
		t.Fatal("processing changed business revision", business, handling, err)
	}
}
