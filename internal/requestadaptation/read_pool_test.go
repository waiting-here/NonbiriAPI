package requestadaptation

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestIndependentLoadsReleaseReadConnectionBeforeDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adaptation.sqlite")
	original, writer := testStoreDriver(t, "sqlite", path)
	writer.SetMaxOpenConns(1)
	if _, err := writer.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	if _, err = reader.Exec("PRAGMA query_only=1"); err != nil {
		t.Fatal(err)
	}
	store, err := New(Config{DB: writer, ReadDB: reader, Codec: original.codec, KeyDeriver: original.deriver})
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{ScopeEndpoint, 1}
	doc := Empty(ScopeEndpoint)
	doc.FixedHeaders.Values["X-Value"] = "first"
	save := func(revision int64, commit bool) {
		t.Helper()
		tx, err := writer.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = store.SaveTx(t.Context(), tx, ref, revision, doc, 1, revision+1); err != nil {
			t.Fatal(err)
		}
		own, err := store.LoadTx(t.Context(), tx, ref)
		if err != nil || own.Revision != strconv.FormatInt(revision+1, 10) {
			t.Fatal("transaction lost own change", err)
		}
		own.Clear()
		if commit {
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}
	save(0, true)
	loads := []func(context.Context) (Snapshot, error){
		func(ctx context.Context) (Snapshot, error) { return store.Load(ctx, ref) },
		func(ctx context.Context) (Snapshot, error) {
			out, err := store.LoadMany(ctx, []Ref{ref})
			if err != nil {
				return Snapshot{}, err
			}
			return out[ref], nil
		},
	}
	held, err := writer.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	for _, load := range loads {
		store.codec = &openHookCodec{GenerationTwoContextCodec: original.codec, afterFirst: func() {
			if reader.Stats().InUse != 0 {
				t.Fatal("decoder retained reader")
			}
		}}
		got, err := load(ctx)
		if err != nil {
			held.Close()
			cancel()
			t.Fatal("load waited for writer", err)
		}
		got.Clear()
	}
	cancel()
	held.Close()
	doc.FixedHeaders.Values["X-Value"] = "second"
	save(1, false)
	got, err := store.Load(t.Context(), ref)
	if err != nil || got.Revision != "1" || got.Document.FixedHeaders.Values["X-Value"] != "first" {
		t.Fatal("rollback escaped", err)
	}
	got.Clear()
	save(1, true)
	got, err = store.Load(t.Context(), ref)
	if err != nil || got.Revision != "2" || got.Document.FixedHeaders.Values["X-Value"] != "second" {
		t.Fatal("committed update invisible", err)
	}
	got.Clear()
	for _, load := range loads {
		canceled, stop := context.WithCancel(t.Context())
		store.codec = &openHookCodec{GenerationTwoContextCodec: original.codec, afterFirst: stop}
		got, err := load(canceled)
		stop()
		if !errors.Is(err, ErrUnavailable) {
			got.Clear()
			t.Fatal("canceled decode returned data", err)
		}
	}
	store.codec = original.codec
	other := Ref{ScopeBinding, 1}
	tx, err := writer.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveTx(t.Context(), tx, other, 0, Empty(ScopeBinding), 1, 1); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec("UPDATE request_adaptations SET secret_ciphertext='invalid' WHERE scope='binding'"); err != nil {
		t.Fatal(err)
	}
	if partial, err := store.LoadMany(t.Context(), []Ref{ref, other}); !errors.Is(err, ErrUnavailable) || partial != nil {
		t.Fatal("failed batch exposed partial settings", err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	for _, load := range loads {
		got, err := load(t.Context())
		if err == nil {
			got.Clear()
			t.Fatal("closed reader used writer")
		}
	}
}
