package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

func TestContinuationRegistryClosedSetAndAuthorityDomains(t *testing.T) {
	store := openMaintenanceStore(t)
	registry := NewRegistry()
	var authorityCalls, snapshotCalls int
	registration := ContinuationRegistration{
		Authority: func(_ context.Context, _ *sql.Tx, request ContinuationRequest) (bool, error) {
			authorityCalls++
			return request.AcceptedRef == "accepted-1", nil
		},
		Snapshot: func(_ context.Context, _ *sql.Tx, request ContinuationRequest) (ContinuationSnapshot, error) {
			snapshotCalls++
			epoch := "7"
			expiresAt := maintenanceTestNow + 15
			var expiry *int64
			if request.Authority == ContinuationSession {
				expiry = &expiresAt
			}
			return ContinuationSnapshot{
				Revision: "9", IdentityEpoch: &epoch, ExpiresAt: expiry,
				Payload: json.RawMessage(`{"state":"accepted","action":"` + request.Action + `"}`),
			}, nil
		},
	}
	if err := registry.Register("rps_session", registration); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("rps_session", registration); err == nil {
		t.Fatal("duplicate continuation kind registered")
	}
	tx := beginMaintenanceTx(t, store.DB())
	sessionRequest := ContinuationRequest{
		Kind: "rps_session", Authority: ContinuationSession, AcceptedRef: "accepted-1",
		ActorUserID: 42, SessionBinding: "session-hash", ResourceRef: "rps-session", Action: "state",
	}
	if _, err := registry.Authorize(context.Background(), tx, sessionRequest); !errors.Is(err, ErrRegistryNotFrozen) {
		t.Fatalf("pre-freeze authorization error=%v", err)
	}
	_ = tx.Rollback()
	if err := registry.Freeze(); err != nil || !registry.Frozen() {
		t.Fatalf("freeze=(%v,%v)", err, registry.Frozen())
	}
	if err := registry.Register("later", registration); !errors.Is(err, ErrRegistryFrozen) {
		t.Fatalf("post-freeze register error=%v", err)
	}

	tx = beginMaintenanceTx(t, store.DB())
	snapshot, err := registry.Authorize(context.Background(), tx, sessionRequest)
	if err != nil {
		t.Fatalf("session continuation: %v", err)
	}
	if snapshot.Revision != "9" || snapshot.IdentityEpoch == nil || *snapshot.IdentityEpoch != "7" {
		t.Fatalf("session snapshot=%+v", snapshot)
	}
	_ = tx.Rollback()

	systemRequest := ContinuationRequest{
		Kind: "rps_session", Authority: ContinuationSystem, AcceptedRef: "accepted-1",
		ResourceRef: "rps-session", Action: "settle",
	}
	tx = beginMaintenanceTx(t, store.DB())
	if _, err := registry.Authorize(context.Background(), tx, systemRequest); err != nil {
		t.Fatalf("system continuation: %v", err)
	}
	_ = tx.Rollback()
	if authorityCalls != 2 || snapshotCalls != 2 {
		t.Fatalf("hook calls authority/snapshot=%d/%d", authorityCalls, snapshotCalls)
	}

	invalidSystem := systemRequest
	invalidSystem.ActorUserID = 42
	invalidSystem.SessionBinding = "forged-session"
	tx = beginMaintenanceTx(t, store.DB())
	if _, err := registry.Authorize(context.Background(), tx, invalidSystem); !errors.Is(err, ErrContinuationDenied) {
		t.Fatalf("system principal smuggling error=%v", err)
	}
	_ = tx.Rollback()
	invalidSession := sessionRequest
	invalidSession.SessionBinding = ""
	tx = beginMaintenanceTx(t, store.DB())
	if _, err := registry.Authorize(context.Background(), tx, invalidSession); !errors.Is(err, ErrContinuationDenied) {
		t.Fatalf("missing session binding error=%v", err)
	}
	_ = tx.Rollback()

	unknown := systemRequest
	unknown.Kind = "unknown"
	tx = beginMaintenanceTx(t, store.DB())
	if _, err := registry.Authorize(context.Background(), tx, unknown); !errors.Is(err, ErrUnknownContinuation) {
		t.Fatalf("unknown kind error=%v", err)
	}
	_ = tx.Rollback()
}

func TestContinuationRegistryDenialAndSnapshotValidation(t *testing.T) {
	store := openMaintenanceStore(t)
	tests := []struct {
		name         string
		registration ContinuationRegistration
		want         error
	}{
		{
			name: "authority denied",
			registration: ContinuationRegistration{
				Authority: func(context.Context, *sql.Tx, ContinuationRequest) (bool, error) { return false, nil },
				Snapshot: func(context.Context, *sql.Tx, ContinuationRequest) (ContinuationSnapshot, error) {
					t.Fatal("snapshot called after authority denial")
					return ContinuationSnapshot{}, nil
				},
			},
			want: ErrContinuationDenied,
		},
		{
			name: "noncanonical revision",
			registration: ContinuationRegistration{
				Authority: func(context.Context, *sql.Tx, ContinuationRequest) (bool, error) { return true, nil },
				Snapshot: func(context.Context, *sql.Tx, ContinuationRequest) (ContinuationSnapshot, error) {
					return ContinuationSnapshot{Revision: "01", Payload: json.RawMessage(`{}`)}, nil
				},
			},
		},
		{
			name: "oversize snapshot",
			registration: ContinuationRegistration{
				Authority: func(context.Context, *sql.Tx, ContinuationRequest) (bool, error) { return true, nil },
				Snapshot: func(context.Context, *sql.Tx, ContinuationRequest) (ContinuationSnapshot, error) {
					payload := make([]byte, MaxContinuationSnapshotBytes+1)
					for index := range payload {
						payload[index] = ' '
					}
					payload[0], payload[len(payload)-1] = '[', ']'
					return ContinuationSnapshot{Revision: "1", Payload: payload}, nil
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.Register("accepted_flow", test.registration); err != nil {
				t.Fatal(err)
			}
			if err := registry.Freeze(); err != nil {
				t.Fatal(err)
			}
			tx := beginMaintenanceTx(t, store.DB())
			_, err := registry.Authorize(context.Background(), tx, ContinuationRequest{
				Kind: "accepted_flow", Authority: ContinuationSystem, AcceptedRef: "accepted",
				ResourceRef: "resource", Action: "continue",
			})
			_ = tx.Rollback()
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Authorize error=%v want %v", err, test.want)
				}
			} else if err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
		})
	}
}
