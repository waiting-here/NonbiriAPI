package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedConnectionProjection(t *testing.T, env *resourceTestEnvironment, owner, id int64, name, note string, revision, createdAt int64) {
	t.Helper()
	if _, err := env.store.DB().Exec(`INSERT INTO service_connections(id,user_id,name,note,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, owner, name, note, revision, createdAt, createdAt+10); err != nil {
		t.Fatal(err)
	}
}

func seedConnectionProjectionEntry(t *testing.T, env *resourceTestEnvironment, owner, connection, id int64, base, note string, createdAt int64) {
	t.Helper()
	if _, err := env.store.DB().Exec(`INSERT INTO endpoints(id,user_id,connection_id,connector_type,base_url,native_service_mask,capability_origin,note,created_at,updated_at) VALUES(?,?,?,'openai-compatible',?,1,'legacy',?,?,?)`, id, owner, connection, base, note, createdAt, createdAt); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionDisplayProjectionPreservesStoredNames(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "connection-display-owner")
	cases := []struct {
		name, storedName, connectionNote, entryNote, want string
		revision, entryOffset, createdOffset              int64
		empty                                             bool
	}{
		{name: "migrated note", entryNote: "  Primary provider  ", want: "Primary provider"},
		{name: "migrated hostname", want: "models.example"},
		{name: "blank note hostname", entryNote: "   ", want: "models.example"},
		{name: "custom name", storedName: "My service", entryNote: "Endpoint label", want: "My service"},
		{name: "edited default", revision: 2, entryNote: "Endpoint label"},
		{name: "connection note", connectionNote: "Owner annotation", entryNote: "Endpoint label"},
		{name: "different entry id", entryOffset: 1, entryNote: "Endpoint label"},
		{name: "different creation time", createdOffset: 1, entryNote: "Endpoint label"},
		{name: "similar default", storedName: "Connection 001", entryNote: "Endpoint label", want: "Connection 001"},
		{name: "empty connection", empty: true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := int64(1000 + i*10)
			storedName := tc.storedName
			if storedName == "" {
				storedName = fmt.Sprintf("Connection %d", id)
			}
			revision := max(tc.revision, 1)
			seedConnectionProjection(t, env, owner, id, storedName, tc.connectionNote, revision, resourceTestNow)
			if !tc.empty {
				seedConnectionProjectionEntry(t, env, owner, id, id+tc.entryOffset, "https://models.example:8443/v1", tc.entryNote, resourceTestNow+tc.createdOffset)
				seedConnectionProjectionEntry(t, env, owner, id, id+2, "https://later.example/v1", "Later provider", resourceTestNow)
			}
			got, err := env.repository.GetConnection(context.Background(), owner, id)
			want := tc.want
			if want == "" {
				want = storedName
			}
			if err != nil || got.DisplayName != want || got.Name != storedName || got.Note != tc.connectionNote || got.Revision != strconv.FormatInt(revision, 10) || got.CreatedAt != resourceTestNow || got.UpdatedAt != resourceTestNow+10 {
				t.Fatalf("connection: %+v, want display %q: %v", got, want, err)
			}
			if tc.empty {
				body, err := json.Marshal(got)
				if err != nil || got.PrimaryEntry != nil || got.EntryCount != "0" || !bytes.Contains(body, []byte(`"primary_entry":null`)) {
					t.Fatalf("empty projection: %s %v", body, err)
				}
			} else if got.PrimaryEntry == nil || got.PrimaryEntry.ID != strconv.FormatInt(id+tc.entryOffset, 10) || got.PrimaryEntry.BaseURL != "https://models.example:8443/v1" || got.PrimaryEntry.Note != tc.entryNote || got.EntryCount != "2" {
				t.Fatalf("primary projection: %+v", got)
			}
			var name, note string
			var storedRevision, created, updated int64
			if err := env.store.DB().QueryRow(`SELECT name,note,revision,created_at,updated_at FROM service_connections WHERE id=?`, id).Scan(&name, &note, &storedRevision, &created, &updated); err != nil || name != storedName || note != tc.connectionNote || storedRevision != revision || created != resourceTestNow || updated != resourceTestNow+10 {
				t.Fatalf("read changed stored connection: %q %q %d %d %d %v", name, note, storedRevision, created, updated, err)
			}
		})
	}
}

func TestConnectionProjectionMutationAndReplay(t *testing.T) {
	f := newConnectionTestFixture(t)
	ctx := context.Background()
	defaultName := fmt.Sprintf("Connection %d", f.connection)
	if _, err := f.env.store.DB().Exec(`UPDATE service_connections SET name=? WHERE id=?`, defaultName, f.connection); err != nil {
		t.Fatal(err)
	}
	seedConnectionProjectionEntry(t, f.env, f.user, f.connection, f.connection, "https://models.example/v1", "Old endpoint label", resourceTestNow)
	before, err := f.env.repository.GetConnection(ctx, f.user, f.connection)
	if err != nil || before.DisplayName != "Old endpoint label" {
		t.Fatalf("before patch: %+v %v", before, err)
	}
	note := "Connection annotation"
	patch := PatchConnectionInput{Note: &note, ExpectedRevision: 1}
	mutation := f.mutation(t, "PATCH", routeConnection, []int64{f.connection}, patch)
	changed, err := f.env.repository.PatchConnection(ctx, f.user, f.connection, mutation, patch)
	if err != nil || changed.Value.Name != defaultName || changed.Value.DisplayName != defaultName || changed.Value.Note != note || changed.Value.Revision != "2" || changed.Value.PrimaryEntry == nil {
		t.Fatalf("patch projection: %+v %v", changed.Value, err)
	}
	replay, err := f.env.repository.PatchConnection(ctx, f.user, f.connection, mutation, patch)
	if err != nil || !replay.Replayed || !bytes.Equal(replay.Body, changed.Body) {
		t.Fatalf("patch replay: %v", err)
	}
	read, err := f.env.repository.GetConnection(ctx, f.user, f.connection)
	if err != nil || read.DisplayName != changed.Value.DisplayName {
		t.Fatalf("patch/read disagreement: %+v %v", read, err)
	}
	if _, err := f.env.repository.PatchConnection(ctx, f.user, f.connection, f.mutation(t, "PATCH", routeConnection, []int64{f.connection}, patch), patch); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	customName := "Renamed provider"
	rename := PatchConnectionInput{Name: &customName, ExpectedRevision: 2}
	renamed, err := f.env.repository.PatchConnection(ctx, f.user, f.connection, f.mutation(t, "PATCH", routeConnection, []int64{f.connection}, rename), rename)
	if err != nil || renamed.Value.Name != customName || renamed.Value.DisplayName != customName || renamed.Value.Revision != "3" {
		t.Fatalf("rename: %+v %v", renamed.Value, err)
	}
	input := ConnectionInput{Name: "Empty service"}
	createMutation := f.mutation(t, "POST", routeConnections, nil, input)
	created, err := f.env.repository.CreateConnection(ctx, f.user, createMutation, input)
	if err != nil || created.Value.DisplayName != input.Name || created.Value.PrimaryEntry != nil || created.Value.Revision != "1" {
		t.Fatalf("created projection: %+v %v", created.Value, err)
	}
	createReplay, err := f.env.repository.CreateConnection(ctx, f.user, createMutation, input)
	if err != nil || !createReplay.Replayed || !bytes.Equal(createReplay.Body, created.Body) {
		t.Fatalf("create replay: %v", err)
	}
}

func TestConnectionSearchOwnedEntriesBeforePagination(t *testing.T) {
	f := newConnectionTestFixture(t)
	env, owner := f.env, f.user
	other := env.seedUser(t, "connection-search-other")
	ctx := context.Background()
	for i := 0; i < 24; i++ {
		id := int64(100 + i)
		seedConnectionProjection(t, env, owner, id, fmt.Sprintf("Service %d", i), "", 1, resourceTestNow)
		seedConnectionProjectionEntry(t, env, owner, id, id*10, "https://first.example/v1", "First entry", resourceTestNow)
		base, note := "https://later.example/v1", "Later entry"
		if i%4 == 0 {
			base = "https://needle.example/v1"
		} else if i%4 == 2 {
			note = "Later Needle annotation"
		}
		seedConnectionProjectionEntry(t, env, owner, id, id*10+1, base, note, resourceTestNow)
		if i == 0 {
			seedConnectionProjectionEntry(t, env, owner, id, id*10+2, base, "Needle duplicate", resourceTestNow)
		}
	}
	seedConnectionProjection(t, env, other, 500, "Other tenant", "", 1, resourceTestNow)
	seedConnectionProjectionEntry(t, env, other, 500, 5000, "https://needle.private.example/v1", "Private tenant marker", resourceTestNow)
	seedConnectionProjection(t, env, owner, 600, "Connection label signal", "Connection note signal", 1, resourceTestNow)
	if _, err := env.store.DB().Exec(`UPDATE credentials SET note='Credential only signal' WHERE id=?`, f.credential); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		total int
	}{
		{" NEEDLE ", 12}, {"Service 23", 1}, {"connection LABEL", 1}, {"connection NOTE", 1},
		{"PRIVATE tenant", 0}, {"Credential only", 0}, {"shared-test-credential-body", 0}, {"%", 0}, {"_", 0},
	} {
		pageRequest := pagination.Request{Page: 1, Size: 10}
		got, err := env.repository.listConnections(ctx, owner, tc.query, connectionListWindow{Page: &pageRequest})
		if err != nil || got.Pagination.TotalItems != strconv.Itoa(tc.total) || len(got.Data) != min(tc.total, 10) {
			t.Fatalf("search %q: %+v %v", tc.query, got, err)
		}
		if tc.total == 12 && (got.Data[0].ID != "122" || got.Data[9].ID != "104" || got.Data[0].PrimaryEntry == nil || got.Data[0].PrimaryEntry.BaseURL != "https://first.example/v1") {
			t.Fatalf("first filtered page: %+v", got)
		}
	}
	pageRequest := pagination.Request{Page: 2, Size: 10}
	last, err := env.repository.listConnections(ctx, owner, "needle", connectionListWindow{Page: &pageRequest})
	if err != nil || last.Pagination.TotalItems != "12" || last.Pagination.TotalPages != "2" || len(last.Data) != 2 || last.Data[0].ID != "102" || last.Data[1].ID != "100" {
		t.Fatalf("second filtered page: %+v %v", last, err)
	}
	pageRequest.Page = 99
	clamped, err := env.repository.listConnections(ctx, owner, "needle", connectionListWindow{Page: &pageRequest})
	if err != nil || clamped.Pagination.Page != "2" || len(clamped.Data) != 2 {
		t.Fatalf("clamped filtered page: %+v %v", clamped, err)
	}
	first, err := env.repository.listConnections(ctx, owner, "needle", connectionListWindow{Limit: 10})
	if err != nil || len(first.Data) != 10 || first.NextCursor == nil {
		t.Fatalf("cursor first page: %+v %v", first, err)
	}
	next, err := env.repository.listConnections(ctx, owner, "needle", connectionListWindow{Limit: 10, Cursor: *first.NextCursor})
	if err != nil || len(next.Data) != 2 || next.NextCursor != nil || next.Data[0].ID != "102" || next.Data[1].ID != "100" {
		t.Fatalf("cursor next page: %+v %v", next, err)
	}
	if _, err := env.repository.listConnections(ctx, owner, "different", connectionListWindow{Limit: 10, Cursor: *first.NextCursor}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cursor search scope: %v", err)
	}
	if _, err := env.repository.GetConnection(ctx, owner, 500); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get tenant isolation: %v", err)
	}
	outsider, err := env.repository.listConnections(ctx, other, "needle", connectionListWindow{Limit: 10})
	if err != nil || len(outsider.Data) != 1 || outsider.Data[0].ID != "500" || outsider.Data[0].PrimaryEntry == nil || outsider.Data[0].PrimaryEntry.ID != "5000" {
		t.Fatalf("other tenant projection: %+v %v", outsider, err)
	}
	query, args := connectionListQuery(owner, "needle")
	for _, plan := range []struct {
		name, query string
		args        []any
	}{
		{"numbered count", `SELECT COUNT(*) FROM (` + query + `)`, args},
		{"numbered rows", query + ` ORDER BY sc.id DESC LIMIT ? OFFSET ?`, append(append([]any{}, args...), 10, 0)},
	} {
		rows, err := env.store.DB().QueryContext(ctx, "EXPLAIN QUERY PLAN "+plan.query, plan.args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(details, "\n")
		t.Logf("%s query plan:\n%s", plan.name, joined)
		if !strings.Contains(joined, "idx_connections_owner") || strings.Count(joined, "idx_endpoints_connection") < 2 || strings.Contains(joined, "SCAN e") {
			t.Fatalf("connection/entry lookup lost indexed ownership scope: %s", joined)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := env.repository.listConnections(cancelled, owner, "needle", connectionListWindow{Limit: 10}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search: %v", err)
	}
}
