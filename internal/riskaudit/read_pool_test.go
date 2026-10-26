package riskaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

type pausedReadAuthority struct {
	FinalAuthorizer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *pausedReadAuthority) pause(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	first := false
	a.once.Do(func() { first = true; close(a.entered) })
	if first {
		select {
		case <-a.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (a *pausedReadAuthority) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.pause(ctx, a.FinalAuthorizer.AuthorizeAdmin(ctx, tx, id))
}
func (a *pausedReadAuthority) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.pause(ctx, a.FinalAuthorizer.AuthorizeStewardMutation(ctx, tx, id))
}

func TestRiskReadPoolWriterProgressAndSnapshot(t *testing.T) {
	f := newAuditFixture(t)
	user := f.user(1)
	removed := f.source(user, "self", "192.0.2.8", "direct_peer", "Client", "model", "success", 0)
	for range 2 {
		f.source(user, "self", "192.0.2.8", "direct_peer", "Client", "model", "success", 0)
	}
	gate := &pausedReadAuthority{FinalAuthorizer: f.authority, entered: make(chan struct{}), release: make(chan struct{})}
	f.repository.finalAuth = gate
	actor := Actor{Admin: true, UserID: f.admin}
	window := Window{From: f.now - 600, To: f.now, Limit: 20, Page: 1}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	type result struct {
		detail UserDetail
		err    error
	}
	done := make(chan result, 1)
	go func() { d, err := f.repository.User(ctx, actor, user, window); done <- result{d, err} }()
	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("reader did not authorize", ctx.Err())
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, time.Second)
	_, err := f.store.DB().ExecContext(writeCtx, "DELETE FROM request_source_facts WHERE request_log_id=?", removed)
	writeCancel()
	if err != nil {
		t.Fatal("reader held the writer connection", err)
	}
	close(gate.release)
	got := <-done
	if got.err != nil || got.detail.Requests.TotalItems != "3" || len(got.detail.Requests.Items) != 3 {
		t.Fatalf("authorization/count/page did not share the original snapshot: %+v %v", got.detail.Requests, got.err)
	}
	next, err := f.repository.User(ctx, actor, user, window)
	if err != nil || next.Requests.TotalItems != "2" || len(next.Requests.Items) != 2 {
		t.Fatalf("next read missed committed change: %+v %v", next.Requests, err)
	}
	if f.store.ReadDB().Stats().InUse != 0 || f.store.DB().Stats().InUse != 0 {
		t.Fatal("connection retained")
	}
}

func TestRiskReadPoolRechecksLivePermissions(t *testing.T) {
	f := newAuditFixture(t)
	id := f.user(5)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["risk.read"] = true
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	f.exec("INSERT INTO steward_permissions(user_id,revision,permissions,limits,created_at,updated_at) VALUES(?,1,?,?,?,?)", id, string(grants), string(limits), f.now, f.now)
	ctx := authz.WithStewardPermission(t.Context(), "risk.read")
	if _, err := f.repository.GetConfig(ctx, Actor{UserID: id}); err != nil {
		t.Fatal(err)
	}
	policy.Permissions["risk.read"] = false
	grants, _ = json.Marshal(policy.Permissions)
	f.exec("UPDATE steward_permissions SET permissions=?,revision=2 WHERE user_id=?", string(grants), id)
	if _, err := f.repository.GetConfig(ctx, Actor{UserID: id}); !errors.Is(err, authz.ErrPermissionDenied) {
		t.Fatal("read reused revoked authority", err)
	}
}

func TestRiskReadPoolCancellationReleasesTransaction(t *testing.T) {
	f := newAuditFixture(t)
	gate := &pausedReadAuthority{FinalAuthorizer: f.authority, entered: make(chan struct{}), release: make(chan struct{})}
	f.repository.finalAuth = gate
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.repository.GetConfig(ctx, Actor{Admin: true, UserID: f.admin}); done <- err }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not enter")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled read succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled reader did not return")
	}
	// database/sql may finish its cancellation rollback after the caller returns.
	// Acquiring the entire pool proves that no connection remains stranded.
	reuseCtx, reuseCancel := context.WithTimeout(t.Context(), time.Second)
	defer reuseCancel()
	connections := make([]*sql.Conn, 0, f.store.ReadDB().Stats().MaxOpenConnections)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for range f.store.ReadDB().Stats().MaxOpenConnections {
		connection, err := f.store.ReadDB().Conn(reuseCtx)
		if err != nil {
			t.Fatal("cancelled transaction retained reader", err)
		}
		connections = append(connections, connection)
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	connections = nil
	f.repository.finalAuth = f.authority
	if _, err := f.repository.GetConfig(t.Context(), Actor{Admin: true, UserID: f.admin}); err != nil {
		t.Fatal(err)
	}
}

func TestRiskReadPoolIsReadOnlyAndMutationsStillCommit(t *testing.T) {
	f := newAuditFixture(t)
	actor := Actor{Admin: true, UserID: f.admin}
	tx, err := f.repository.begin(t.Context(), actor, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(t.Context(), "UPDATE risk_audit_config SET threshold_percent=threshold_percent WHERE id=1")
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("read transaction allowed a write")
	}
	config, err := f.repository.GetConfig(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	config.ThresholdPercent++
	saved, err := f.repository.UpdateConfig(t.Context(), actor, config)
	if err != nil || saved.ThresholdPercent != config.ThresholdPercent {
		t.Fatal(saved, err)
	}
	current, err := f.repository.CurrentConfig(t.Context())
	if err != nil || current != saved {
		t.Fatal(current, err)
	}
}
