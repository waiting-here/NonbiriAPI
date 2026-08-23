package donation

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type recordingLimiter struct {
	mu       sync.Mutex
	forgot   [][]int64
	restored [][]int64
}

func (r *recordingLimiter) take() (forgot, restored [][]int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	forgot, restored = append([][]int64(nil), r.forgot...), append([][]int64(nil), r.restored...)
	r.forgot, r.restored = nil, nil
	for _, batch := range forgot {
		sort.Slice(batch, func(i, j int) bool { return batch[i] < batch[j] })
	}
	for _, batch := range restored {
		sort.Slice(batch, func(i, j int) bool { return batch[i] < batch[j] })
	}
	return
}

func (r *recordingLimiter) ForgetDonationKeys(ids ...int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgot = append(r.forgot, append([]int64(nil), ids...))
}
func (r *recordingLimiter) RestoreDonationKeys(ids ...int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restored = append(r.restored, append([]int64(nil), ids...))
}

func TestReviewReconcilesLimiterFromCurrentActiveProjection(t *testing.T) {
	f := newDonationFixture(t)
	lim := new(recordingLimiter)
	svc := NewService(ServiceDeps{Store: f.st, Limiter: lim})
	user := f.userID(t, "limiter")
	d, err := f.st.CreateDonation(context.Background(), db.CreateDonationInput{
		UserID: user, Description: "x", Now: 10,
		New: &db.NewEndpointSpec{ConnectorType: "openai-compatible", BaseURL: "https://api.example.com"},
		Keys: []db.NewKeySpec{
			{Secret: []byte("sk-limiter-a"), DisplayHead: "a", DisplayTail: "a"},
			{Secret: []byte("sk-limiter-b"), DisplayHead: "b", DisplayTail: "b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := f.st.ListDonationKeyLimiterStates(context.Background(), d.ID, 20)
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys: %v (%d)", err, len(keys))
	}
	ids := []int64{keys[0].ID, keys[1].ID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	approve := db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionApprove, Now: 20}
	if _, err := svc.Review(context.Background(), approve); err != nil {
		t.Fatal(err)
	}
	forgot, restored := lim.take()
	if len(forgot) != 0 || len(restored) != 1 || !reflect.DeepEqual(restored[0], ids) {
		t.Fatalf("approve calls forgot=%v restored=%v, want restore=%v", forgot, restored, ids)
	}
	off := false
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionUpdate, Now: 30,
		KeyUpdates: []db.DonationKeyUpdate{{DonationKeyID: ids[0], Enabled: &off}}}); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 1 || !reflect.DeepEqual(forgot[0], []int64{ids[0]}) || len(restored) != 1 || !reflect.DeepEqual(restored[0], []int64{ids[1]}) {
		t.Fatalf("single-key disable calls forgot=%v restored=%v", forgot, restored)
	}
	on := true
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionUpdate, Now: 40,
		KeyUpdates: []db.DonationKeyUpdate{{DonationKeyID: ids[0], Enabled: &on}}}); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 0 || len(restored) != 1 || !reflect.DeepEqual(restored[0], ids) {
		t.Fatalf("single-key enable calls forgot=%v restored=%v", forgot, restored)
	}
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionDisable, Now: 50}); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 1 || !reflect.DeepEqual(forgot[0], ids) || len(restored) != 0 {
		t.Fatalf("whole donation disable calls forgot=%v restored=%v", forgot, restored)
	}
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionEnable, Now: 55}); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 0 || len(restored) != 1 || !reflect.DeepEqual(restored[0], ids) {
		t.Fatalf("whole donation enable calls forgot=%v restored=%v", forgot, restored)
	}
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionDisable, Now: 58}); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 1 || !reflect.DeepEqual(forgot[0], ids) || len(restored) != 0 {
		t.Fatalf("second whole donation disable calls forgot=%v restored=%v", forgot, restored)
	}
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: user, Action: db.ReviewActionDisable, Now: 60}); err == nil {
		t.Fatal("repeated disable unexpectedly succeeded")
	}
	forgot, restored = lim.take()
	if len(forgot) != 0 || len(restored) != 0 {
		t.Fatalf("failed review emitted hooks forgot=%v restored=%v", forgot, restored)
	}
	if err := svc.DeleteAsReviewer(context.Background(), d.ID, db.ReviewRoleAdmin, user); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 1 || !reflect.DeepEqual(forgot[0], ids) || len(restored) != 0 {
		t.Fatalf("delete calls forgot=%v restored=%v", forgot, restored)
	}
}

func TestDonationLimiterProjectionExpires(t *testing.T) {
	f := newDonationFixture(t)
	uid := f.userID(t, "expiry")
	d, err := f.st.CreateDonation(context.Background(), db.CreateDonationInput{
		UserID: uid, Description: "expires", ExpiresAt: ptrInt64(30), Now: 10,
		New:  &db.NewEndpointSpec{ConnectorType: "openai-compatible", BaseURL: "https://api.example.com"},
		Keys: []db.NewKeySpec{{Secret: []byte("sk-expired"), DisplayHead: "e", DisplayTail: "d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.ApplyDonationReview(context.Background(), db.ReviewDecision{DonationID: d.ID, Role: db.ReviewRoleAdmin, ReviewerID: uid, Action: db.ReviewActionApprove, Now: 15}); err != nil {
		t.Fatal(err)
	}
	states, err := f.st.ListDonationKeyLimiterStates(context.Background(), d.ID, 15)
	if err != nil || len(states) != 1 || !states[0].Active {
		t.Fatalf("live projection = %+v, err=%v", states, err)
	}
	states, err = f.st.ListDonationKeyLimiterStates(context.Background(), d.ID, 31)
	if err != nil || len(states) != 1 || states[0].Active {
		t.Fatalf("expired projection = %+v, err=%v", states, err)
	}
	if _, err := f.st.ListDonationKeyLimiterStates(context.Background(), d.ID, 0); err == nil {
		t.Fatal("zero projection clock unexpectedly succeeded")
	}
	// Use a separate approved donation to exercise the service post-commit
	// path, with the same fixed clock crossing its expiry.
	d2, err := f.st.CreateDonation(context.Background(), db.CreateDonationInput{
		UserID: uid, Description: "service expiry", ExpiresAt: ptrInt64(30), Now: 10,
		New:  &db.NewEndpointSpec{ConnectorType: "openai-compatible", BaseURL: "https://api.example.com"},
		Keys: []db.NewKeySpec{{Secret: []byte("sk-service-expiry"), DisplayHead: "e", DisplayTail: "y"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lim := new(recordingLimiter)
	clock := int64(15)
	svc := NewService(ServiceDeps{Store: f.st, Limiter: lim, Now: func() int64 { return clock }})
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d2.ID, Role: db.ReviewRoleAdmin, ReviewerID: uid, Action: db.ReviewActionApprove, Now: 15}); err != nil {
		t.Fatal(err)
	}
	forgot, restored := lim.take()
	if len(forgot) != 0 || len(restored) != 1 {
		t.Fatalf("approve calls forgot=%v restored=%v", forgot, restored)
	}
	clock = 31
	if _, err := svc.Review(context.Background(), db.ReviewDecision{DonationID: d2.ID, Role: db.ReviewRoleAdmin, ReviewerID: uid, Action: db.ReviewActionUpdate, Now: 31}); err != nil {
		t.Fatal(err)
	}
	forgot, restored = lim.take()
	if len(forgot) != 1 || len(restored) != 0 {
		t.Fatalf("expiry reconcile calls forgot=%v restored=%v", forgot, restored)
	}
}

func ptrInt64(v int64) *int64 { return &v }
