package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
)

func TestProductionRetirementBoundaryExcludesDeletingRequestAndRestoresOnAbort(t *testing.T) {
	gate, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
	if err != nil {
		t.Fatalf("lifecyclegate.New: %v", err)
	}
	flow, err := flowcontrol.New(flowcontrol.Config{MaxConcurrentUsers: 4})
	if err != nil {
		t.Fatalf("flowcontrol.New: %v", err)
	}
	t.Cleanup(func() { _ = flow.Close() })
	games, err := game.NewStartLimiter(game.StartLimiterConfig{MaxUsers: 4})
	if err != nil {
		t.Fatalf("game.NewStartLimiter: %v", err)
	}
	t.Cleanup(func() { _ = games.Close() })

	boundary := &productionRetirementBoundary{gate: gate, flow: flow, games: games}
	const userID int64 = 41
	validate := func(context.Context, int64, string) (bool, error) { return true, nil }

	deleteContext, releaseDelete, err := gate.Admit(context.Background(), userID, "delete-session", validate)
	if err != nil {
		t.Fatalf("admit deleting request: %v", err)
	}
	defer releaseDelete()
	otherContext, releaseOther, err := gate.Admit(context.Background(), userID, "other-session", validate)
	if err != nil {
		t.Fatalf("admit competing request: %v", err)
	}
	drained := make(chan struct{})
	go func() {
		<-otherContext.Done()
		releaseOther()
		close(drained)
	}()

	retirement, err := boundary.BeginUserRetirement(deleteContext, userID)
	if err != nil {
		t.Fatalf("begin retirement: %v", err)
	}
	if err := deleteContext.Err(); err != nil {
		t.Fatalf("deleting request was cancelled: %v", err)
	}
	select {
	case <-drained:
	default:
		t.Fatal("competing request was not drained before retirement returned")
	}
	if _, _, err := gate.Admit(context.Background(), userID, "late-session", validate); !errors.Is(err, lifecyclegate.ErrRetiring) {
		t.Fatalf("late lifecycle admission error = %v, want ErrRetiring", err)
	}
	if _, _, err := games.Reserve(userID); !errors.Is(err, game.ErrUserDeleting) {
		t.Fatalf("game start during retirement error = %v, want ErrUserDeleting", err)
	}

	flowStarted := make(chan struct{})
	flowResult := make(chan error, 1)
	go func() {
		close(flowStarted)
		reservation, _, admitErr := flow.Admit(context.Background(), userID)
		if reservation != nil {
			reservation.Release()
		}
		flowResult <- admitErr
	}()
	<-flowStarted
	select {
	case admitErr := <-flowResult:
		t.Fatalf("forward admission crossed active retirement: %v", admitErr)
	case <-time.After(25 * time.Millisecond):
	}

	if !retirement.Abort() {
		t.Fatal("first abort returned false")
	}
	if retirement.Abort() || retirement.Commit() {
		t.Fatal("retirement terminal action was not one-shot")
	}
	select {
	case admitErr := <-flowResult:
		if admitErr != nil {
			t.Fatalf("forward admission after abort: %v", admitErr)
		}
	case <-time.After(time.Second):
		t.Fatal("forward admission did not resume after abort")
	}
	lateContext, releaseLate, err := gate.Admit(context.Background(), userID, "late-session", validate)
	if err != nil {
		t.Fatalf("lifecycle admission after abort: %v", err)
	}
	if err := lateContext.Err(); err != nil {
		t.Fatalf("lifecycle admission after abort was cancelled: %v", err)
	}
	releaseLate()
	start, _, err := games.Reserve(userID)
	if err != nil {
		t.Fatalf("game start after abort: %v", err)
	}
	start.Release()
}
