//go:build linux && amd64

package db_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	_ "modernc.org/sqlite"
)

const (
	scaleEnabledEnv  = "NONBIRI_STARTUP_SCALE"
	scaleCaseEnv     = "NONBIRI_STARTUP_SCALE_CASE"
	scaleChildMode   = "NONBIRI_STARTUP_SCALE_CHILD"
	scaleChildPath   = "NONBIRI_STARTUP_SCALE_PATH"
	scaleChildUsers  = "NONBIRI_STARTUP_SCALE_USERS"
	scaleChildRows   = "NONBIRI_STARTUP_SCALE_ENTRIES"
	scaleChildKeys   = "NONBIRI_STARTUP_SCALE_KEYS"
	scaleOldOrphans  = 150
	scaleNewOrphans  = 150
	scaleLiveKeys    = 1000
	scaleLedgerBatch = 250
	scaleExt4Magic   = 0xEF53
)

type startupScaleCase struct {
	name    string
	users   int
	entries int
	keys    int
}

func TestStartupScale(t *testing.T) {
	if os.Getenv(scaleEnabledEnv) != "1" {
		t.Skip("set NONBIRI_STARTUP_SCALE=1 for the explicit Linux ext4 scale gate")
	}
	if os.Getenv(scaleChildMode) != "" {
		t.Fatal("parent scale test was selected in a child process")
	}
	cases := []startupScaleCase{
		{name: "fresh"},
		{name: "100k_entries_1k_users", users: 1000, entries: 100000, keys: scaleLiveKeys},
		{name: "1m_entries_10k_users", users: 10000, entries: 1000000, keys: scaleLiveKeys},
	}
	filter := os.Getenv(scaleCaseEnv)
	selected := false
	for _, item := range cases {
		if filter != "" && filter != item.name {
			continue
		}
		selected = true
		t.Run(item.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scale.sqlite")
			dbtest.EnsureOwnerOnlyParent(t, path)
			requireScaleExt4(t, filepath.Dir(path))
			if item.entries > 0 {
				started := time.Now()
				buildStartupScaleFixture(t, path, item)
				t.Logf("fixture_generation_ms=%d users=%d ledger_entries=%d linked_credentials=%d old_orphans=%d unmarked_orphans=%d", time.Since(started).Milliseconds(), item.users, item.entries, item.keys, scaleOldOrphans, scaleNewOrphans)
				walStarted := time.Now()
				runStartupScaleChild(t, "wal", path, item)
				for _, suffix := range []string{"-wal", "-shm"} {
					info, err := os.Stat(path + suffix)
					if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
						t.Fatalf("committed WAL fixture missing %s: info=%v err=%v", suffix, info, err)
					}
					t.Logf("fixture_sidecar=%s bytes=%d", suffix, info.Size())
				}
				t.Logf("wal_fixture_ms=%d", time.Since(walStarted).Milliseconds())
			} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("fresh fixture was not absent: %v", err)
			}
			if info, err := os.Stat(path); err == nil {
				t.Logf("fixture_main_bytes=%d", info.Size())
			}
			// Separate child processes isolate startup peak RSS from the much
			// larger fixture generator and from the previous restart.
			runStartupScaleChild(t, "open", path, item)
			runStartupScaleChild(t, "open", path, item)
		})
	}
	if !selected {
		t.Fatalf("unknown %s=%q", scaleCaseEnv, filter)
	}
}

func requireScaleExt4(t *testing.T, dir string) {
	t.Helper()
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		t.Fatalf("inspect scale fixture filesystem: %v", err)
	}
	if stat.Type != scaleExt4Magic {
		t.Fatalf("scale fixture requires native ext4, filesystem type=0x%x; set TMPDIR to an ext4 directory", stat.Type)
	}
	t.Logf("fixture_filesystem_type=0x%x available_bytes=%d", stat.Type, stat.Bavail*uint64(stat.Bsize))
}

func startupScaleVault(t *testing.T) *secret.Vault {
	t.Helper()
	key := bytes.Repeat([]byte{0x67}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create synthetic scale vault: %v", err)
	}
	return vault
}

func buildStartupScaleFixture(t *testing.T, path string, item startupScaleCase) {
	t.Helper()
	if item.entries <= 0 || item.entries%2 != 0 || item.users <= 0 || item.keys != scaleLiveKeys {
		t.Fatal("invalid scale fixture dimensions")
	}
	dbfixture.Materialize(t, path)
	vault := startupScaleVault(t)
	defer func() { _ = vault.Close() }()
	startup, cancelStartup := context.WithTimeout(context.Background(), db.DefaultStartupTimeout)
	store, err := db.OpenContext(startup, path, vault)
	cancelStartup()
	if err != nil {
		t.Fatalf("open source fixture before population: %v", err)
	}
	defer func() { _ = store.Close() }()
	at := time.Now().Unix()
	fixtureContext := t.Context()
	users, wallets := seedStartupScaleUsers(t, fixtureContext, store.DB(), item.users, at)
	seedStartupScaleCredentials(t, fixtureContext, store.DB(), vault, users, at)
	seedStartupScaleLedger(t, fixtureContext, store.DB(), users[0], wallets, item.entries/2, at)
	var count int
	if err := store.DB().QueryRowContext(fixtureContext, `SELECT COUNT(*) FROM credit_entries`).Scan(&count); err != nil || count != item.entries {
		t.Fatalf("fixture ledger rows=%d want=%d: %v", count, item.entries, err)
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), db.DefaultShutdownTimeout)
	defer cancelShutdown()
	if err := store.CloseContext(shutdown); err != nil {
		t.Fatalf("close populated fixture: %v", err)
	}
}

func seedStartupScaleUsers(t *testing.T, ctx context.Context, database *sql.DB, count int, at int64) ([]int64, []int64) {
	t.Helper()
	users := make([]int64, 0, count)
	wallets := make([]int64, 0, count)
	zero := db.EncodeU128(db.U128{})
	for start := 0; start < count; start += 500 {
		end := min(start+500, count)
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin user fixture batch: %v", err)
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
total_unknown_usage_requests,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("prepare user fixture batch: %v", err)
		}
		for i := start; i < end; i++ {
			admin := 0
			if i == 0 {
				admin = 1
			}
			label := fmt.Sprintf("scale-user-%06d", i)
			result, err := stmt.ExecContext(ctx, label, label, admin, zero, zero, zero, zero, zero, zero, zero, zero, at, at)
			if err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				t.Fatalf("insert user %d: %v", i, err)
			}
			userID, err := result.LastInsertId()
			if err != nil || userID <= 0 {
				_ = stmt.Close()
				_ = tx.Rollback()
				t.Fatalf("read user %d id: %v", i, err)
			}
			general, err := ledger.CreateUserAssetAccount(ctx, tx, userID, ledger.General, at)
			if err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				t.Fatalf("create general wallet for user %d: %v", i, err)
			}
			if _, err := ledger.CreateUserAssetAccount(ctx, tx, userID, ledger.Game, at); err != nil {
				_ = stmt.Close()
				_ = tx.Rollback()
				t.Fatalf("create game wallet for user %d: %v", i, err)
			}
			users = append(users, userID)
			wallets = append(wallets, general.ID)
		}
		if err := stmt.Close(); err != nil {
			_ = tx.Rollback()
			t.Fatalf("close user fixture statement: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit user fixture batch: %v", err)
		}
	}
	return users, wallets
}

func seedStartupScaleCredentials(t *testing.T, ctx context.Context, database *sql.DB, vault *secret.Vault, users []int64, at int64) {
	t.Helper()
	const baseURL = "https://upstream.example.com/v1"
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin credential fixture: %v", err)
	}
	defer tx.Rollback()
	for i := 0; i < scaleLiveKeys+scaleOldOrphans+scaleNewOrphans; i++ {
		var contextID [16]byte
		copy(contextID[:8], "scale-v1")
		binary.BigEndian.PutUint64(contextID[8:], uint64(i+1))
		credentialContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID[:])
		if err != nil {
			t.Fatalf("credential context %d: %v", i, err)
		}
		plaintext := []byte(fmt.Sprintf("synthetic-scale-credential-%d", i))
		fingerprint := sha256.Sum256(plaintext)
		envelope, err := vault.SealForGenerationTwoContext(plaintext, credentialContext)
		clear(plaintext)
		if err != nil {
			t.Fatalf("encrypt credential %d: %v", i, err)
		}
		var orphaned any
		if i >= scaleLiveKeys && i < scaleLiveKeys+scaleOldOrphans {
			orphaned = at - 7200
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at,orphaned_at)
VALUES(?,?,'openai-compatible',?,?,?)`, contextID[:], baseURL, envelope, at-8000, orphaned)
		if err != nil {
			t.Fatalf("insert encrypted credential %d: %v", i, err)
		}
		secretID, err := result.LastInsertId()
		if err != nil || secretID <= 0 {
			t.Fatalf("read encrypted credential %d id: %v", i, err)
		}
		if i >= scaleLiveKeys {
			continue
		}
		endpoint, err := tx.ExecContext(ctx, `INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,?,?)`, users[i%len(users)], baseURL, at, at)
		if err != nil {
			t.Fatalf("insert linked endpoint %d: %v", i, err)
		}
		endpointID, err := endpoint.LastInsertId()
		if err != nil || endpointID <= 0 {
			t.Fatalf("read endpoint %d id: %v", i, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'synthetic','scale','',1,0,1,?,?)`, endpointID, secretID, fingerprint[:], at, at); err != nil {
			t.Fatalf("link encrypted credential %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit credential fixture: %v", err)
	}
}

func seedStartupScaleLedger(t *testing.T, ctx context.Context, database *sql.DB, actor int64, wallets []int64, operations int, at int64) {
	t.Helper()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("read external account: %v", err)
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("find external account: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("end external account read: %v", err)
	}
	for start := 0; start < operations; start += scaleLedgerBatch {
		end := min(start+scaleLedgerBatch, operations)
		if err := seedStartupScaleLedgerBatch(ctx, database, actor, wallets, external.ID, start, end, at); err != nil {
			t.Fatalf("apply ledger operations %d..%d: %v", start, end, err)
		}
		if end%(100000/2) == 0 || end == operations {
			t.Logf("fixture_ledger_entries_committed=%d", end*2)
		}
	}
}

func seedStartupScaleLedgerBatch(ctx context.Context, database *sql.DB, actor int64, wallets []int64, external int64, start, end int, at int64) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := start; i < end; i++ {
		operationID, err := db.GenerateOpaqueID("op_")
		if err != nil {
			return err
		}
		plan, err := ledger.NewAdminUserAdjustment(
			ledger.Meta{OperationID: operationID, ActorUserID: actor, CreatedAt: at},
			wallets[i%len(wallets)], external, ledger.AmountFromMilli(1), 0, ledger.Amount{}, "synthetic scale allocation")
		if err != nil {
			return err
		}
		if _, err := ledger.Apply(ctx, tx, plan); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func runStartupScaleChild(t *testing.T, mode, path string, item startupScaleCase) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestStartupScaleChildProcess$", "-test.v", "-test.timeout=7m")
	command.Env = append(os.Environ(),
		scaleChildMode+"="+mode,
		scaleChildPath+"="+path,
		scaleChildUsers+"="+strconv.Itoa(item.users),
		scaleChildRows+"="+strconv.Itoa(item.entries),
		scaleChildKeys+"="+strconv.Itoa(item.keys))
	output, err := command.CombinedOutput()
	t.Logf("child_mode=%s output:\n%s", mode, strings.TrimSpace(string(output)))
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("scale child mode=%s exit=%d", mode, exit.ExitCode())
		}
		t.Fatalf("start scale child mode=%s: %v", mode, err)
	}
	t.Logf("child_mode=%s exit_code=0", mode)
}

func TestStartupScaleChildProcess(t *testing.T) {
	mode := os.Getenv(scaleChildMode)
	if mode == "" {
		t.Skip("scale child helper")
	}
	path := os.Getenv(scaleChildPath)
	if path == "" {
		t.Fatal("scale child path missing")
	}
	switch mode {
	case "wal":
		leaveStartupScaleWAL(t, path)
		os.Exit(0) // Preserve SQLite's genuine committed WAL and SHM sidecars.
	case "open":
		users := startupScaleDimension(t, scaleChildUsers)
		entries := startupScaleDimension(t, scaleChildRows)
		keys := startupScaleDimension(t, scaleChildKeys)
		openStartupScaleChild(t, path, users, entries, keys)
	default:
		t.Fatalf("unknown scale child mode %q", mode)
	}
}

func startupScaleDimension(t *testing.T, name string) int {
	t.Helper()
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 0 {
		t.Fatalf("invalid scale child dimension %s", name)
	}
	return value
}

func leaveStartupScaleWAL(t *testing.T, path string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "rw")
	u.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatalf("open WAL child database: %v", err)
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mode string
	if err := database.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL mode=%q err=%v", mode, err)
	}
	if _, err := database.ExecContext(ctx, `PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatalf("disable WAL autocheckpoint: %v", err)
	}
	result, err := database.ExecContext(ctx, `UPDATE site_config SET updated_at=updated_at+1 WHERE key='site_name'`)
	if err != nil {
		t.Fatalf("commit WAL child update: %v", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		t.Fatalf("WAL child changed=%d err=%v", changed, err)
	}
}

func openStartupScaleChild(t *testing.T, path string, users, entries, keys int) {
	t.Helper()
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
		t.Fatalf("read initial resource usage: %v", err)
	}
	vault := startupScaleVault(t)
	defer func() { _ = vault.Close() }()
	started := time.Now()
	trace := db.NewStartupTrace(started, func(progress db.StartupProgress) {
		t.Logf("stage=%s elapsed_ms=%d stage_elapsed_ms=%d processed_count=%d processed_bytes=%d committed_checkpoint=%s",
			progress.Stage, progress.ElapsedMS, progress.StageElapsedMS,
			progress.ProcessedCount, progress.ProcessedBytes, progress.LastCommittedPhase)
	})
	ctx, cancel := context.WithTimeout(context.Background(), db.DefaultStartupTimeout)
	ctx = trace.Context(ctx)
	store, err := db.OpenContext(ctx, path, vault)
	cancel()
	openDuration := time.Since(started)
	if err != nil {
		t.Fatalf("OpenContext(default budget=%s) elapsed=%s stage=%s: %v", db.DefaultStartupTimeout, openDuration, trace.Snapshot().Stage, err)
	}
	if openDuration > db.DefaultStartupTimeout {
		_ = store.Close()
		t.Fatalf("OpenContext exceeded default startup budget: %s", openDuration)
	}
	assertStartupScaleRows(t, store.DB(), users, entries, keys)
	closeStarted := time.Now()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), db.DefaultShutdownTimeout)
	err = store.CloseContext(shutdown)
	cancelShutdown()
	closeDuration := time.Since(closeStarted)
	if err != nil || closeDuration > db.DefaultShutdownTimeout {
		t.Fatalf("CloseContext elapsed=%s budget=%s err=%v", closeDuration, db.DefaultShutdownTimeout, err)
	}
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
		t.Fatalf("read final resource usage: %v", err)
	}
	progress := trace.Snapshot()
	t.Logf("open_ms=%d close_ms=%d max_rss_kib=%d cpu_user_ms=%d cpu_system_ms=%d final_stage=%s processed_count=%d processed_bytes=%d committed_checkpoint=%s",
		openDuration.Milliseconds(), closeDuration.Milliseconds(), after.Maxrss,
		rusageMilliseconds(after.Utime)-rusageMilliseconds(before.Utime),
		rusageMilliseconds(after.Stime)-rusageMilliseconds(before.Stime),
		progress.Stage, progress.ProcessedCount, progress.ProcessedBytes, progress.LastCommittedPhase)
}

func rusageMilliseconds(value syscall.Timeval) int64 {
	return value.Sec*1000 + value.Usec/1000
}

func assertStartupScaleRows(t *testing.T, database *sql.DB, users, entries, keys int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, item := range []struct {
		name  string
		query string
		want  int
	}{
		{"users", `SELECT COUNT(*) FROM users`, users},
		{"credit_operations", `SELECT COUNT(*) FROM credit_operations`, entries / 2},
		{"credit_entries", `SELECT COUNT(*) FROM credit_entries`, entries},
		{"linked_endpoint_keys", `SELECT COUNT(*) FROM endpoint_keys`, keys},
		{"live_encrypted_credentials", `SELECT COUNT(*) FROM endpoint_key_secrets WHERE orphaned_at IS NULL`, keys},
	} {
		var count int
		if err := database.QueryRowContext(ctx, item.query).Scan(&count); err != nil || count != item.want {
			t.Fatalf("%s=%d want=%d: %v", item.name, count, item.want, err)
		}
	}
	if entries == 0 {
		return
	}
	var pending int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoint_key_secrets WHERE orphaned_at IS NOT NULL`).Scan(&pending); err != nil || pending != scaleNewOrphans {
		t.Fatalf("recoverable recent credential markers=%d want=%d: %v", pending, scaleNewOrphans, err)
	}
	var obsolete int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM endpoint_key_secrets WHERE orphaned_at <= ?`, time.Now().Unix()-3600).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("expired orphan credentials left after recovery=%d: %v", obsolete, err)
	}
}
