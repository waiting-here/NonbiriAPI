package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

// The driver blocks inside rollback after database/sql has marked the
// transaction done, reproducing the cancellation interval at shutdown.
type closingConnector struct {
	rollbackStarted  chan struct{}
	finishRollback   chan struct{}
	connectionClosed chan struct{}
}

func (c *closingConnector) Connect(context.Context) (driver.Conn, error) { return closingConn{c}, nil }
func (c *closingConnector) Driver() driver.Driver                        { return closingDriver{} }

type closingDriver struct{}

func (closingDriver) Open(string) (driver.Conn, error) { return nil, errors.New("connector required") }

type closingConn struct{ probe *closingConnector }

func (c closingConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused prepare") }
func (c closingConn) Close() error                        { close(c.probe.connectionClosed); return nil }
func (c closingConn) Begin() (driver.Tx, error)           { return closingTx{c.probe}, nil }

type closingTx struct{ probe *closingConnector }

func (closingTx) Commit() error { return errors.New("unused commit") }
func (tx closingTx) Rollback() error {
	close(tx.probe.rollbackStarted)
	<-tx.probe.finishRollback
	return nil
}

func TestStoreCloseWaitsForCancelledTransactionRollback(t *testing.T) {
	probe := &closingConnector{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	database := sql.OpenDB(probe)
	database.SetMaxOpenConns(1)
	store := &Store{db: database}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		select {
		case <-probe.finishRollback:
		default:
			close(probe.finishRollback)
		}
		_ = database.Close()
	})
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-probe.rollbackStarted:
	case <-time.After(time.Second):
		t.Fatal("rollback did not start")
	}
	if err := tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("second rollback=%v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	var premature bool
	select {
	case err := <-closed:
		premature = true
		if err != nil {
			t.Errorf("close=%v", err)
		}
	case <-time.After(50 * time.Millisecond):
	}
	close(probe.finishRollback)
	if !premature {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("close did not drain rollback")
		}
	}
	select {
	case <-probe.connectionClosed:
	case <-time.After(time.Second):
		t.Fatal("underlying connection not closed")
	}
	if premature {
		t.Fatal("store closed before the cancelled transaction finished rolling back")
	}
	if stats := database.Stats(); stats.OpenConnections != 0 {
		t.Fatalf("open connections=%d", stats.OpenConnections)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second close=%v", err)
	}
}
