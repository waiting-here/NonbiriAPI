package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	DatabaseApplicationID uint32 = 0x4E425249
	DatabaseUserVersion   uint32 = 2
)

// StartupErrorKind is a stable, non-sensitive database startup category.
type StartupErrorKind string

const (
	StartupUnsafePath       StartupErrorKind = "unsafe_path"
	StartupIncompleteFresh  StartupErrorKind = "incomplete_fresh"
	StartupRollbackJournal  StartupErrorKind = "rollback_journal_present"
	StartupInvalidHeader    StartupErrorKind = "invalid_header"
	StartupWrongIdentity    StartupErrorKind = "wrong_identity"
	StartupWrongGeneration  StartupErrorKind = "wrong_generation"
	StartupSourceChanged    StartupErrorKind = "source_changed"
	StartupCorruptDatabase  StartupErrorKind = "corrupt_database"
	StartupSchemaMismatch   StartupErrorKind = "schema_mismatch"
	StartupCredentialReject StartupErrorKind = "credential_rejected"
	StartupInitialization   StartupErrorKind = "initialization_failed"
	StartupCleanupFailure   StartupErrorKind = "cleanup_failed"
	StartupReadIO           StartupErrorKind = "read_io"
	StartupWriteIO          StartupErrorKind = "write_io"
	StartupSQLBusy          StartupErrorKind = "sql_busy"
)

var ErrDatabaseStartup = errors.New("database startup rejected")

// StartupError intentionally carries no path, SQL, endpoint, row identifier,
// ciphertext, or cryptographic detail. Generation is safe to expose only for
// the marker mismatch category.
type StartupError struct {
	Kind               StartupErrorKind
	ExpectedGeneration uint32
	ActualGeneration   uint32
	Reason             string
}

func (e *StartupError) Error() string {
	if e == nil {
		return ErrDatabaseStartup.Error()
	}
	if e.Kind == StartupWrongGeneration {
		return fmt.Sprintf("database startup rejected: %s (expected generation %d, actual %d)", e.Kind, e.ExpectedGeneration, e.ActualGeneration)
	}
	return "database startup rejected: " + string(e.Kind)
}

func (e *StartupError) Unwrap() error { return ErrDatabaseStartup }

func startupError(kind StartupErrorKind) error { return &StartupError{Kind: kind} }

func preserveStartupCategory(err error, fallback StartupErrorKind) error {
	if err == nil {
		return nil
	}
	var startupErr *StartupError
	if errors.As(err, &startupErr) {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if category, _, _ := StartupFailureDetails(err); category == "sql_busy" {
		return startupError(StartupSQLBusy)
	}
	return startupError(fallback)
}

func generationError(actual uint32) error {
	return &StartupError{Kind: StartupWrongGeneration, ExpectedGeneration: DatabaseUserVersion, ActualGeneration: actual}
}

type sourceFileSnapshot struct {
	path   string
	role   string
	file   *os.File
	info   os.FileInfo
	size   int64
	mtime  int64
	mode   os.FileMode
	digest [sha256.Size]byte
}

type sourceSnapshotSet struct {
	main    *sourceFileSnapshot
	journal *sourceFileSnapshot
	wal     *sourceFileSnapshot
	shm     *sourceFileSnapshot
}

func (s *sourceSnapshotSet) close() error {
	if s == nil {
		return nil
	}
	var result error
	for _, item := range []*sourceFileSnapshot{s.main, s.journal, s.wal, s.shm} {
		if item != nil && item.file != nil {
			if err := item.file.Close(); err != nil {
				result = startupError(StartupCleanupFailure)
			}
			item.file = nil
		}
	}
	return result
}

func captureSourceSet(ctx context.Context, path string) (*sourceSnapshotSet, error) {
	set := &sourceSnapshotSet{}
	var err error
	if set.main, err = captureSourceFile(ctx, path, "database file"); err != nil {
		return nil, appendStartupError(err, set.close())
	}
	if set.journal, err = captureSourceFile(ctx, path+"-journal", "rollback journal file"); err != nil {
		return nil, appendStartupError(err, set.close())
	}
	if set.wal, err = captureSourceFile(ctx, path+"-wal", "wal file"); err != nil {
		return nil, appendStartupError(err, set.close())
	}
	if set.shm, err = captureSourceFile(ctx, path+"-shm", "shm file"); err != nil {
		return nil, appendStartupError(err, set.close())
	}
	return set, nil
}

func captureSourceFile(ctx context.Context, path, role string) (snapshot *sourceFileSnapshot, result error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lstat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, startupIOError(ctx, err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return nil, startupError(StartupUnsafePath)
	}
	f, err := openReadOnlyNoFollow(path)
	if err != nil {
		return nil, startupIOError(ctx, err)
	}
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			if err := f.Close(); err != nil {
				result = appendStartupError(result, startupError(StartupCleanupFailure))
			}
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return nil, startupIOError(ctx, err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(lstat, info) {
		return nil, startupError(StartupUnsafePath)
	}
	if err := validateSourceFile(f, info); err != nil {
		return nil, startupError(StartupUnsafePath)
	}
	h := sha256.New()
	if _, err := copyWithContext(ctx, h, f); err != nil {
		return nil, startupIOError(ctx, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, startupIOError(ctx, err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	closeOnFailure = false
	return &sourceFileSnapshot{
		path: path, role: role, file: f, info: info, size: info.Size(),
		mtime: info.ModTime().UnixNano(), mode: info.Mode(), digest: digest,
	}, nil
}

func equalSourceFile(a, b *sourceFileSnapshot) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a.info, b.info) && a.size == b.size && a.mtime == b.mtime &&
		a.mode == b.mode && a.digest == b.digest
}

func recheckSourceSet(ctx context.Context, path string, original *sourceSnapshotSet) (result error) {
	RecordStartupStage(ctx, StageSourceRecheck)
	current, err := captureSourceSet(ctx, path)
	if err != nil {
		return err
	}
	defer func() { result = appendStartupError(result, current.close()) }()
	for i, before := range []*sourceFileSnapshot{original.main, original.journal, original.wal, original.shm} {
		after := []*sourceFileSnapshot{current.main, current.journal, current.wal, current.shm}[i]
		if reason := sourceChangeReason(before, after); reason != "" {
			return &StartupError{Kind: StartupSourceChanged, Reason: reason}
		}
	}
	return nil
}

func openGenerationTwo(ctx context.Context, path string, secrets secret.GenerationTwoContextCodec) (store *Store, result error) {
	initial, err := captureSourceSet(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { result = appendStartupError(result, initial.close()) }()
	// A rollback journal may contain uncommitted dirty pages whose recovery
	// would write the source database. Fresh-only startup never opens, copies,
	// recovers, or deletes it, regardless of whether SQLite would call it hot.
	if initial.journal != nil {
		return nil, startupError(StartupRollbackJournal)
	}
	if initial.main == nil {
		fresh := initial.journal == nil && initial.wal == nil && initial.shm == nil
		if err := initial.close(); err != nil {
			return nil, err
		}
		if !fresh {
			return nil, startupError(StartupIncompleteFresh)
		}
		return createFreshGenerationTwo(ctx, path, secrets)
	}
	if err := validateCurrentSnapshot(ctx, path, initial, secrets); err != nil {
		return nil, err
	}
	return openValidatedSource(ctx, path, initial, secrets)
}

func validateHeader(main *sourceFileSnapshot) error {
	if main == nil {
		return startupError(StartupInvalidHeader)
	}
	// An empty file is the distinctive incomplete-fresh marker.  Preserve
	// that category so callers can distinguish it from a non-SQLite or
	// truncated existing database without opening it for recovery.
	if main.size == 0 {
		return startupError(StartupIncompleteFresh)
	}
	if main.size < 100 {
		return startupError(StartupInvalidHeader)
	}
	var header [100]byte
	n, err := main.file.ReadAt(header[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return startupError(StartupReadIO)
	}
	if n != len(header) || string(header[:16]) != "SQLite format 3\x00" {
		return startupError(StartupInvalidHeader)
	}
	actualGeneration := binary.BigEndian.Uint32(header[60:64])
	if binary.BigEndian.Uint32(header[68:72]) != DatabaseApplicationID {
		return startupError(StartupWrongIdentity)
	}
	if actualGeneration != DatabaseUserVersion {
		return generationError(actualGeneration)
	}
	return nil
}

type validationWorkspace struct {
	dir      string
	mainPath string
	dirInfo  os.FileInfo
	owned    map[string]ownedFileEvidence
}

type ownedFileEvidence struct {
	info  os.FileInfo
	size  int64
	mtime int64
	mode  os.FileMode
}

func makeOwnedFileEvidence(info os.FileInfo) ownedFileEvidence {
	return ownedFileEvidence{info: info, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: info.Mode()}
}

func (e ownedFileEvidence) matches(info os.FileInfo) bool {
	return e.info != nil && os.SameFile(e.info, info) && e.size == info.Size() &&
		e.mtime == info.ModTime().UnixNano() && e.mode == info.Mode()
}

// matchesIdentity is used only for the O_EXCL-created fresh main database.
// SQLite necessarily changes its size and timestamps while the fresh schema
// is being built, but it must not change the underlying file identity.  The
// sidecar evidence continues to use matches, since a sidecar has no equivalent
// pre-creation identity proof.
func (e ownedFileEvidence) matchesIdentity(info os.FileInfo) bool {
	return e.info != nil && info != nil && os.SameFile(e.info, info)
}

func newValidationWorkspace() (*validationWorkspace, error) {
	dir, err := os.MkdirTemp("", "nonbiri-db-validate-")
	if err != nil {
		return nil, startupError(StartupInitialization)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.Remove(dir)
		return nil, startupError(StartupInitialization)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() {
		_ = os.Remove(dir)
		return nil, startupError(StartupInitialization)
	}
	return &validationWorkspace{
		dir: dir, mainPath: filepath.Join(dir, "database.sqlite"), dirInfo: dirInfo,
		owned: make(map[string]ownedFileEvidence),
	}, nil
}

func (w *validationWorkspace) recordOwned(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return startupError(StartupInitialization)
	}
	w.owned[filepath.Base(path)] = makeOwnedFileEvidence(info)
	return nil
}

func (w *validationWorkspace) recordSQLiteSidecars() error {
	for _, suffix := range []string{"-wal", "-shm"} {
		path := w.mainPath + suffix
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return startupError(StartupInitialization)
		}
		name := filepath.Base(path)
		if prior, ok := w.owned[name]; ok && !os.SameFile(prior.info, info) {
			return startupError(StartupSourceChanged)
		}
		w.owned[name] = makeOwnedFileEvidence(info)
	}
	return nil
}

func (w *validationWorkspace) cleanup() error {
	if w == nil || w.dir == "" {
		return nil
	}
	currentDir, err := os.Lstat(w.dir)
	if err != nil || !currentDir.IsDir() || !os.SameFile(w.dirInfo, currentDir) {
		return startupError(StartupCleanupFailure)
	}
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return startupError(StartupCleanupFailure)
	}
	allowed := map[string]bool{
		"database.sqlite": true, "database.sqlite-wal": true, "database.sqlite-shm": true,
	}
	for _, entry := range entries {
		owned, ok := w.owned[entry.Name()]
		if !allowed[entry.Name()] || !ok {
			return startupError(StartupCleanupFailure)
		}
		info, err := os.Lstat(filepath.Join(w.dir, entry.Name()))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !owned.matches(info) {
			return startupError(StartupCleanupFailure)
		}
	}
	for _, name := range []string{"database.sqlite-shm", "database.sqlite-wal", "database.sqlite"} {
		p := filepath.Join(w.dir, name)
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return startupError(StartupCleanupFailure)
		}
		owned, ok := w.owned[name]
		if !ok || !owned.matches(info) {
			return startupError(StartupCleanupFailure)
		}
		if err := os.Remove(p); err != nil {
			return startupError(StartupCleanupFailure)
		}
	}
	if err := os.Remove(w.dir); err != nil {
		return startupError(StartupCleanupFailure)
	}
	w.dir = ""
	return nil
}

func copySnapshot(ctx context.Context, source *sourceFileSnapshot, destination string) (result error) {
	if source == nil || source.file == nil {
		return startupError(StartupSourceChanged)
	}
	if _, err := source.file.Seek(0, io.SeekStart); err != nil {
		return startupIOError(ctx, err)
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return startupError(StartupWriteIO)
	}
	createdInfo, err := out.Stat()
	if err != nil {
		_ = out.Close()
		return startupError(StartupInitialization)
	}
	copyOK := false
	outClosed := false
	defer func() {
		if !copyOK {
			if !outClosed {
				if err := out.Close(); err != nil {
					result = appendStartupError(result, startupError(StartupCleanupFailure))
				}
			}
			if current, err := os.Lstat(destination); err == nil && current.Mode().IsRegular() &&
				current.Mode()&os.ModeSymlink == 0 && os.SameFile(createdInfo, current) {
				if err := os.Remove(destination); err != nil {
					result = appendStartupError(result, startupError(StartupCleanupFailure))
				}
			}
		}
	}()
	written, err := copyWithContext(ctx, out, source.file)
	if err != nil {
		return startupIOError(ctx, err)
	}
	if written != source.size {
		return &StartupError{Kind: StartupSourceChanged, Reason: "size"}
	}
	if err := out.Sync(); err != nil {
		return startupError(StartupWriteIO)
	}
	outClosed = true
	if err := out.Close(); err != nil {
		return startupError(StartupCleanupFailure)
	}
	copied, err := os.Open(destination)
	if err != nil {
		return startupIOError(ctx, err)
	}
	h := sha256.New()
	_, hashErr := copyWithContext(ctx, h, copied)
	closeErr := copied.Close()
	if hashErr != nil {
		if closeErr != nil {
			return appendStartupError(startupIOError(ctx, hashErr), startupError(StartupCleanupFailure))
		}
		return startupIOError(ctx, hashErr)
	}
	if closeErr != nil {
		return startupError(StartupCleanupFailure)
	}
	if !equalDigest(h.Sum(nil), source.digest) {
		return &StartupError{Kind: StartupSourceChanged, Reason: "digest"}
	}
	copyOK = true
	return nil
}

func equalDigest(sum []byte, expected [sha256.Size]byte) bool {
	return len(sum) == len(expected) && string(sum) == string(expected[:])
}

func validateCurrentSnapshot(ctx context.Context, path string, source *sourceSnapshotSet, secrets secret.GenerationTwoContextCodec) error {
	RecordStartupStage(ctx, StageSnapshotCopy)
	workspace, err := newValidationWorkspace()
	if err != nil {
		return err
	}
	var validationErr error
	if err := copySnapshot(ctx, source.main, workspace.mainPath); err != nil {
		validationErr = err
	} else if err := workspace.recordOwned(workspace.mainPath); err != nil {
		validationErr = err
	} else if source.wal != nil {
		validationErr = copySnapshot(ctx, source.wal, workspace.mainPath+"-wal")
		if validationErr == nil {
			validationErr = workspace.recordOwned(workspace.mainPath + "-wal")
		}
	}
	if validationErr == nil {
		if afterSnapshotCopyHook != nil {
			afterSnapshotCopyHook()
		}
		validationErr = recheckSourceSet(ctx, path, source)
	}
	if validationErr == nil {
		// The source path is never opened for a raw header read. Validate the
		// copied main bytes first, then let the read-only SQLite validation see
		// the copied main+WAL view. This keeps marker classification inside the
		// private snapshot interval and ensures it cannot perform source writes.
		validationErr = validateHeaderCopy(workspace.mainPath)
	}
	copyOwnsCleanup := validationErr == nil
	if copyOwnsCleanup {
		RecordStartupStage(ctx, StageSnapshotValidation)
		validationErr = validateReadOnlyCopy(ctx, workspace, secrets)
	}
	// On success SQLite and its private workspace have closed. On cancellation
	// its registered close operation retains cleanup ownership until it finishes.
	recordStartupFailure(ctx, validationErr)
	validationErr = appendStartupError(validationErr, recheckSourceSet(ctx, path, source))
	if !copyOwnsCleanup {
		validationErr = appendStartupError(validationErr, workspace.cleanup())
	}
	return validationErr
}

// validateHeaderCopy reads only the private main-file copy created for
// current-database preflight. The source descriptor is intentionally not
// passed here: DEC-009 treats the local filesystem as trusted, while the
// startup contract still requires header/generation classification before any
// SQLite read-only open of the source data.
func validateHeaderCopy(path string) (result error) {
	file, err := os.Open(path)
	if err != nil {
		return startupError(StartupReadIO)
	}
	defer func() {
		if err := file.Close(); err != nil {
			result = appendStartupError(result, startupError(StartupCleanupFailure))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return startupError(StartupReadIO)
	}
	if !info.Mode().IsRegular() {
		return startupError(StartupInvalidHeader)
	}
	return validateHeader(&sourceFileSnapshot{file: file, size: info.Size()})
}

func sqliteFileURI(path, mode string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	u := url.URL{Scheme: "file", Path: slash}
	query := u.Query()
	query.Set("mode", mode)
	query.Set("cache", "private")
	// Apply connection-local protections again whenever the pool replaces a connection.
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func openSQLite(path, mode string) (*sql.DB, error) {
	dsn, err := sqliteFileURI(path, mode)
	if err != nil {
		return nil, err
	}
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	return d, nil
}

func validateReadOnlyCopy(ctx context.Context, workspace *validationWorkspace, secrets secret.GenerationTwoContextCodec) (result error) {
	d, err := openSQLiteContext(ctx, workspace.mainPath, "ro")
	if err != nil {
		return appendStartupError(startupSQLFailure(ctx, err, StartupCorruptDatabase), workspace.cleanup())
	}
	copyStore := &Store{db: d, afterClose: func() error {
		return appendStartupError(workspace.recordSQLiteSidecars(), workspace.cleanup())
	}}
	defer func() {
		result = appendStartupError(result, copyStore.CloseContext(ctx))
	}()
	if _, err := d.ExecContext(ctx, `PRAGMA query_only=ON; PRAGMA foreign_keys=ON;`); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if err := workspace.recordSQLiteSidecars(); err != nil {
		return err
	}
	// Page one can itself be committed in the WAL. The raw source header is the
	// pre-SQLite gate; these PRAGMAs verify the merged main+WAL view.
	var applicationID, userVersion uint32
	if err := d.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if applicationID != DatabaseApplicationID {
		return startupError(StartupWrongIdentity)
	}
	if err := d.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if userVersion != DatabaseUserVersion {
		return generationError(userVersion)
	}
	if err := foreignKeyCheck(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if err := quickCheck(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	prior, err := generationTwoExtensionNeeded(ctx, d)
	if err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	priorAssets := false
	if prior {
		var columns int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('credit_accounts') WHERE name='asset_type'`).Scan(&columns); err != nil {
			return startupSQLFailure(ctx, err, StartupSchemaMismatch)
		}
		priorAssets = columns == 0
	}
	if err := validateVersionSeedManifest(ctx, d, priorAssets, prior); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	if err := validateEndpointKeyEnvelopes(ctx, d, secrets); err != nil {
		return startupSQLFailure(ctx, err, StartupCredentialReject)
	}
	if err := validateAssetSourceConfig(ctx, d, prior); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	return nil
}

func quickCheck(ctx context.Context, q queryer) error {
	rows, err := q.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil || result != "ok" {
			return errors.New("quick check failed")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != 1 {
		return errors.New("quick check did not return exactly one row")
	}
	return nil
}

func foreignKeyCheck(ctx context.Context, q queryer) error {
	rows, err := q.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign key check failed")
	}
	return rows.Err()
}

func validateEndpointKeyEnvelopes(ctx context.Context, q queryer, generationTwoCodec secret.GenerationTwoContextCodec) error {
	if generationTwoCodec == nil {
		return errors.New("generation-two secret codec unavailable")
	}
	// Validate every secret row, including orphaned rows. A join against the
	// live endpoint-key set is insufficient: an unreferenced ciphertext is
	// still persisted secret material and must have an authenticated Gen2
	// context before the database can become writable.
	rows, err := q.QueryContext(ctx, `
SELECT id, context_id, canonical_base_url, connector_type,
       typeof(encrypted_secret), length(CAST(encrypted_secret AS BLOB)),
       CASE WHEN typeof(encrypted_secret)='text'
                  AND length(CAST(encrypted_secret AS BLOB)) BETWEEN 1 AND ?
            THEN encrypted_secret END,
       orphaned_at,
       (SELECT COUNT(*) FROM endpoint_keys ek WHERE ek.secret_ref_id=endpoint_key_secrets.id),
       (SELECT COUNT(*) FROM dispatch_claims c WHERE c.secret_ref_id=endpoint_key_secrets.id AND c.state IN ('claimed','dispatched'))
FROM endpoint_key_secrets
ORDER BY id`, maxEndpointCredentialEnvelopeBytes)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var contextID []byte
		var canonicalBaseURL, connector, ciphertextType string
		var ciphertext sql.NullString
		var ciphertextBytes int64
		var orphanedAt sql.NullInt64
		var endpointKeyRefs, pendingClaimRefs int
		if err := rows.Scan(&id, &contextID, &canonicalBaseURL, &connector, &ciphertextType, &ciphertextBytes, &ciphertext, &orphanedAt, &endpointKeyRefs, &pendingClaimRefs); err != nil {
			clear(contextID)
			return err
		}
		validRow := id > 0 && len(contextID) == 16 && utf8.ValidString(canonicalBaseURL) &&
			(connector == "openai-compatible" || connector == "anthropic-compatible" || connector == "ai-sdk-gateway-v3") &&
			ciphertextType == "text" && ciphertextBytes >= 1 && ciphertextBytes <= maxEndpointCredentialEnvelopeBytes &&
			ciphertext.Valid && int64(len([]byte(ciphertext.String))) == ciphertextBytes && utf8.ValidString(ciphertext.String) &&
			(!orphanedAt.Valid || (orphanedAt.Int64 >= 0 && orphanedAt.Int64 <= generationTwoMaxUnixSeconds))
		if !validRow {
			clear(contextID)
			ciphertext.String = ""
			return errors.New("invalid endpoint credential row")
		}
		storedBaseURL, originErr := egress.CanonicalEndpointBaseURL(canonicalBaseURL)
		if originErr != nil || storedBaseURL != canonicalBaseURL {
			clear(contextID)
			ciphertext.String = ""
			return errors.New("invalid endpoint credential origin")
		}
		credentialContext, contextErr := secret.NewGenerationTwoEndpointKeyContext(contextID)
		clear(contextID)
		if contextErr != nil {
			ciphertext.String = ""
			return errors.New("invalid endpoint credential context")
		}
		version, versionErr := secret.ParseEnvelopeVersion(ciphertext.String)
		if versionErr != nil || version != secret.EnvelopeVersionV2 {
			ciphertext.String = ""
			return errors.New("invalid endpoint credential envelope")
		}
		plaintext, openErr := generationTwoCodec.OpenForGenerationTwoContext(ciphertext.String, credentialContext)
		ciphertext.String = ""
		clear(plaintext)
		if openErr != nil {
			return errors.New("invalid endpoint credential authentication")
		}
		recordStartupProgress(ctx, 1, 0)
		if orphanedAt.Valid && (endpointKeyRefs != 0 || pendingClaimRefs != 0) {
			return errors.New("orphaned endpoint credential remains referenced")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	// Validate each live endpoint-key link separately. This catches identity
	// drift (endpoint origin/connector versus immutable secret metadata) even
	// when the envelope itself remains cryptographically valid.
	rows, err = q.QueryContext(ctx, `
SELECT ek.id, ek.secret_ref_id,
       typeof(e.base_url), length(CAST(e.base_url AS BLOB)),
       CASE WHEN typeof(e.base_url)='text'
                  AND length(CAST(e.base_url AS BLOB)) BETWEEN 1 AND ?
            THEN e.base_url END,
       e.connector_type, eks.canonical_base_url, eks.connector_type, eks.orphaned_at
FROM endpoint_keys ek
JOIN endpoints e ON e.id=ek.endpoint_id
JOIN endpoint_key_secrets eks ON eks.id=ek.secret_ref_id
ORDER BY ek.id`, maxStoredEndpointBaseURLBytes)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var keyID, secretRefID int64
		var baseType, endpointConnector, secretBaseURL, secretConnector string
		var baseBytes int64
		var baseURL sql.NullString
		var orphanedAt sql.NullInt64
		if err := rows.Scan(&keyID, &secretRefID, &baseType, &baseBytes, &baseURL, &endpointConnector, &secretBaseURL, &secretConnector, &orphanedAt); err != nil {
			return err
		}
		if keyID <= 0 || secretRefID <= 0 || baseType != "text" || baseBytes < 1 || baseBytes > maxStoredEndpointBaseURLBytes ||
			!baseURL.Valid || int64(len([]byte(baseURL.String))) != baseBytes || !utf8.ValidString(baseURL.String) ||
			(endpointConnector != "openai-compatible" && endpointConnector != "anthropic-compatible" && endpointConnector != "ai-sdk-gateway-v3") ||
			secretConnector != endpointConnector || orphanedAt.Valid {
			baseURL.String = ""
			return errors.New("invalid endpoint credential link")
		}
		target, _, targetErr := egress.CanonicalEndpointTarget(baseURL.String)
		secretTarget, _, secretTargetErr := egress.CanonicalEndpointTarget(secretBaseURL)
		baseURL.String = ""
		if targetErr != nil || secretTargetErr != nil || target != secretTarget {
			return errors.New("invalid endpoint credential link origin")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	// A non-terminal claim keeps its secret live even if its endpoint key has
	// already been detached. Such a claim must never point at an orphan marker.
	var invalidClaims int
	if err := q.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM dispatch_claims c
JOIN endpoint_key_secrets s ON s.id=c.secret_ref_id
WHERE c.state IN ('claimed','dispatched') AND s.orphaned_at IS NOT NULL`).Scan(&invalidClaims); err != nil {
		return err
	}
	if invalidClaims != 0 {
		return errors.New("pending dispatch claim references orphaned credential")
	}
	return nil
}

const generationTwoMaxUnixSeconds int64 = 253402300799

// reconcileEndpointKeySecretOrphans performs the bounded writable recovery
// step for detached endpoint-key secret rows. A detached row remains live
// while a claimed/dispatched request references it; otherwise it receives an
// orphan marker and is swept after one hour. Each transaction handles at most
// 100 rows and has its own two-second deadline. The stable id order and the
// frozen decision time let the loop converge before the listener opens without
// imposing a one-batch cap on a legitimate account deletion.
func reconcileEndpointKeySecretOrphans(ctx context.Context, d *sql.DB) error {
	if d == nil {
		return errors.New("endpoint credential recovery: database unavailable")
	}
	if ctx == nil {
		return errors.New("endpoint credential recovery: context is required")
	}
	now := time.Now().Unix()
	if now < 0 || now > generationTwoMaxUnixSeconds {
		return errors.New("endpoint credential recovery: clock outside contract")
	}
	cutoff := now - 3600
	if cutoff < 0 {
		cutoff = 0
	}
	for {
		batchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		tx, err := d.BeginTx(batchCtx, nil)
		if err != nil {
			cancel()
			return fmt.Errorf("endpoint credential recovery: begin: %w", err)
		}
		committed := false
		batchFailure := func(batchErr error) error {
			if !committed {
				_ = tx.Rollback()
			}
			cancel()
			return batchErr
		}
		deleteResult, err := tx.ExecContext(batchCtx, `DELETE FROM endpoint_key_secrets
			WHERE id IN (SELECT s.id FROM endpoint_key_secrets s
			            WHERE s.orphaned_at IS NOT NULL AND s.orphaned_at <= ?
			              AND NOT EXISTS (SELECT 1 FROM endpoint_keys ek WHERE ek.secret_ref_id=s.id)
			              AND NOT EXISTS (SELECT 1 FROM dispatch_claims c
			                              WHERE c.secret_ref_id=s.id AND c.state IN ('claimed','dispatched'))
			            ORDER BY s.id LIMIT 100)`, cutoff)
		if err != nil {
			return batchFailure(fmt.Errorf("endpoint credential recovery: sweep detached rows: %w", err))
		}
		deleted, err := deleteResult.RowsAffected()
		if err != nil || deleted < 0 || deleted > 100 {
			if err != nil {
				return batchFailure(fmt.Errorf("endpoint credential recovery: invalid sweep count: %w", err))
			}
			return batchFailure(errors.New("endpoint credential recovery: invalid sweep count"))
		}
		remaining := int64(100) - deleted
		marked := int64(0)
		if remaining > 0 {
			markResult, markErr := tx.ExecContext(batchCtx, `UPDATE endpoint_key_secrets
				SET orphaned_at=?
				WHERE id IN (SELECT s.id FROM endpoint_key_secrets s
				            WHERE s.orphaned_at IS NULL
				              AND NOT EXISTS (SELECT 1 FROM endpoint_keys ek WHERE ek.secret_ref_id=s.id)
				              AND NOT EXISTS (SELECT 1 FROM dispatch_claims c
				                              WHERE c.secret_ref_id=s.id AND c.state IN ('claimed','dispatched'))
				            ORDER BY s.id LIMIT ?)`, now, remaining)
			if markErr != nil {
				return batchFailure(fmt.Errorf("endpoint credential recovery: mark detached rows: %w", markErr))
			}
			marked, err = markResult.RowsAffected()
			if err != nil || marked < 0 || marked > remaining {
				if err != nil {
					return batchFailure(fmt.Errorf("endpoint credential recovery: invalid mark count: %w", err))
				}
				return batchFailure(errors.New("endpoint credential recovery: invalid mark count"))
			}
		}
		if err := tx.Commit(); err != nil {
			return batchFailure(fmt.Errorf("endpoint credential recovery: commit: %w", err))
		}
		committed = true
		RecordStartupCheckpoint(ctx, StageDomainRecovery)
		recordStartupProgress(ctx, deleted+marked, 0)
		cancel()
		if deleted+marked == 0 {
			return nil
		}
	}
}

func validateWritableGenerationTwoState(ctx context.Context, d *sql.DB, secrets secret.GenerationTwoContextCodec) error {
	if d == nil {
		return startupError(StartupInitialization)
	}
	var applicationID, userVersion uint32
	if err := d.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if applicationID != DatabaseApplicationID {
		return startupError(StartupWrongIdentity)
	}
	if err := d.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if userVersion != DatabaseUserVersion {
		return generationError(userVersion)
	}
	if err := foreignKeyCheck(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if err := quickCheck(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupCorruptDatabase)
	}
	if err := validateGenerationTwoManifest(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	if err := validateGenerationTwoSeedManifest(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	if _, err := validateCurrentGenerationTwoSiteConfig(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupSchemaMismatch)
	}
	if err := validateEndpointKeyEnvelopes(ctx, d, secrets); err != nil {
		return startupSQLFailure(ctx, err, StartupCredentialReject)
	}
	return nil
}

var beforeWritableOpenHook func()
var afterSnapshotCopyHook func()
var beforeFreshExclusiveCreateHook func()
var freshSchemaFailureHook func() error

// writableRecoveryPhaseHook is a bounded, pre-listener recovery seam. The
// domain workers may install their recovery implementation, but the ordering
// and the SQLite integrity checks remain owned by this package.
var writableRecoveryPhaseHook func(context.Context, *sql.DB) error

func checkpointAndRecoverBeforeListener(ctx context.Context, d *sql.DB, secrets secret.GenerationTwoContextCodec) error {
	if d == nil {
		return startupError(StartupInitialization)
	}
	var busy, logFrames, checkpointed int
	if err := d.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return startupSQLFailure(ctx, err, StartupInitialization)
	}
	if busy != 0 {
		return startupError(StartupSQLBusy)
	}
	if logFrames < 0 || checkpointed < 0 || checkpointed > logFrames {
		return startupError(StartupInitialization)
	}
	if err := foreignKeyCheck(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupInitialization)
	}
	if err := quickCheck(ctx, d); err != nil {
		return startupSQLFailure(ctx, err, StartupInitialization)
	}
	if writableRecoveryPhaseHook != nil {
		if err := writableRecoveryPhaseHook(ctx, d); err != nil {
			return preserveStartupCategory(err, StartupInitialization)
		}
	}
	// The hook runs before the listener and is allowed to repair only through
	// this narrow seam. Re-run every integrity and current-state gate after it;
	// a hook must never be able to mutate a valid database into an invalid one
	// and still open the listener.
	if err := foreignKeyCheck(ctx, d); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if err := quickCheck(ctx, d); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if err := reconcileEndpointKeySecretOrphans(ctx, d); err != nil {
		return preserveStartupCategory(err, StartupInitialization)
	}
	if err := validateWritableGenerationTwoState(ctx, d, secrets); err != nil {
		return err
	}
	return nil
}

func openValidatedSource(ctx context.Context, path string, expected *sourceSnapshotSet, secrets secret.GenerationTwoContextCodec) (*Store, error) {
	if beforeWritableOpenHook != nil {
		beforeWritableOpenHook()
	}
	// Finish every source descriptor operation before SQLite can take POSIX
	// locks. Even a read-only descriptor's close could release those locks.
	if err := recheckSourceSet(ctx, path, expected); err != nil {
		return nil, err
	}
	if err := secureDBFiles(path); err != nil {
		return nil, startupError(StartupUnsafePath)
	}
	if err := expected.close(); err != nil {
		return nil, err
	}
	RecordStartupStage(ctx, StageRawHandlesClosed)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	RecordStartupStage(ctx, StageSourceOpen)
	d, err := openSQLiteContext(ctx, path, "rw")
	if err != nil {
		return nil, startupSQLFailure(ctx, err, StartupInitialization)
	}
	opened := sourceStore(ctx, d, secrets)
	failError := func(err error) (*Store, error) {
		return nil, appendStartupError(preserveStartupCategory(err, StartupInitialization), opened.CloseContext(ctx))
	}
	if _, err := d.ExecContext(ctx, `PRAGMA foreign_keys=ON;`); err != nil {
		return failError(startupSQLFailure(ctx, err, StartupInitialization))
	}
	var journalMode string
	if err := d.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil || !strings.EqualFold(journalMode, "wal") {
		if err == nil {
			err = startupError(StartupInitialization)
		}
		return failError(startupSQLFailure(ctx, err, StartupInitialization))
	}
	RecordStartupCheckpoint(ctx, StageSourceOpen)
	if err := inspectActiveDBFiles(path); err != nil {
		return failError(startupError(StartupUnsafePath))
	}
	RecordStartupStage(ctx, StageSchemaUpgrade)
	if err := extendKnownGenerationTwoSchema(ctx, d); err != nil {
		return failError(startupSQLFailure(ctx, err, StartupSchemaMismatch))
	}
	RecordStartupCheckpoint(ctx, StageSchemaUpgrade)
	RecordStartupStage(ctx, StageDomainRecovery)
	if err := checkpointAndRecoverBeforeListener(ctx, d, secrets); err != nil {
		return failError(err)
	}
	return opened, nil
}

type freshOwnership struct {
	path  string
	files map[string]ownedFileEvidence
}

func requireFreshSidecarsAbsent(path string) error {
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return startupError(StartupUnsafePath)
		}
		return startupError(StartupSourceChanged)
	}
	return nil
}

func newFreshOwnership(path string, mainInfo os.FileInfo) *freshOwnership {
	return &freshOwnership{path: path, files: map[string]ownedFileEvidence{filepath.Base(path): makeOwnedFileEvidence(mainInfo)}}
}

func (o *freshOwnership) capturePresent() error {
	for _, candidate := range []string{o.path, o.path + "-journal", o.path + "-wal", o.path + "-shm"} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return startupError(StartupCleanupFailure)
		}
		name := filepath.Base(candidate)
		if prior, ok := o.files[name]; ok && !os.SameFile(prior.info, info) {
			return startupError(StartupCleanupFailure)
		}
		if _, ok := o.files[name]; !ok {
			// A sidecar first observed after the O_EXCL claim is not
			// attributable to this handle: another creator can win the
			// interval between the absence check and SQLite's open. Do not
			// adopt it into the cleanup set. The caller fails closed and
			// cleanup consequently cannot remove an unknown sidecar.
			return startupError(StartupSourceChanged)
		}
	}
	return nil
}

func (o *freshOwnership) cleanup() error {
	if o == nil || o.path == "" {
		return startupError(StartupCleanupFailure)
	}
	var cleanupErr error
	for _, candidate := range []string{o.path + "-shm", o.path + "-wal", o.path + "-journal", o.path} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		owned, ok := o.files[filepath.Base(candidate)]
		if !ok {
			// Unknown sidecars are deliberately left untouched.  capturePresent
			// reports their presence as source_changed, but cleanup must still
			// remove the O_EXCL-proven main file independently.
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			if cleanupErr == nil {
				cleanupErr = startupError(StartupCleanupFailure)
			}
			continue
		}
		matches := owned.matches(info)
		if filepath.Base(candidate) == filepath.Base(o.path) {
			matches = owned.matchesIdentity(info)
		}
		if !matches {
			if cleanupErr == nil {
				cleanupErr = startupError(StartupCleanupFailure)
			}
			continue
		}
		if err := os.Remove(candidate); err != nil && cleanupErr == nil {
			cleanupErr = startupError(StartupCleanupFailure)
		}
	}
	return cleanupErr
}

func seedGenerationTwo(ctx context.Context, tx *sql.Tx, announcementEpoch string) error {
	zero := make([]byte, 16)
	if err := insertGenerationTwoConfig(ctx, tx, announcementEpoch); err != nil {
		return err
	}
	progressionPresent, err := ProgressionStoragePresent(ctx, tx)
	if err != nil {
		return err
	}
	if !progressionPresent {
		for key := range progressionConfigDefaults() {
			if _, err := tx.ExecContext(ctx, `DELETE FROM site_config WHERE key=?`, key); err != nil {
				return err
			}
		}
	}
	governancePresent, err := GovernanceStoragePresent(ctx, tx)
	if err != nil {
		return err
	}
	if !governancePresent {
		for key := range governanceConfigDefaults() {
			if _, err := tx.ExecContext(ctx, `DELETE FROM site_config WHERE key=?`, key); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE site_config SET value='' WHERE key='level_display_name_5'`); err != nil {
			return err
		}
	}
	for _, domain := range []string{"site", "activities", "games"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO config_revisions(domain,revision,updated_at) VALUES(?,?,0)`, domain, 1); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_usage_totals(id,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,updated_at) VALUES(1,?,?,?,?,?,?,?,0)`, zero, zero, zero, zero, zero, zero, zero); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credit_capacity(id,last_ledger_seq,reserved_future_rows,revision) VALUES(1,0,?,?)`, zero, zero); err != nil {
		return err
	}
	accountIDs := make(map[string]int64, 6)
	for _, account := range []struct{ code, kind string }{
		{code: "platform", kind: "platform"},
		{code: "external", kind: "external"},
		{code: "forward_reserve", kind: "platform"},
		{code: "charity_reserve", kind: "platform"},
		{code: "game_fishing_reserve", kind: "platform"},
	} {
		result, err := tx.ExecContext(ctx, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES(?,?,0,?,?,0)`, account.kind, account.code, zero, 0)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		accountIDs[account.code] = id
	}
	for _, pool := range []struct{ id, typ string }{{"", "welfare"}, {"", "thursday"}} {
		poolID, err := GenerateOpaqueID("pol_")
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('pool',?,0,?,?,0)`, "pool:"+poolID, zero, 0)
		if err != nil {
			return err
		}
		accountID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO shared_pools(id,pool_type,account_id,state,revision,created_at) VALUES(?,?,?,'open',1,0)`, poolID, pool.typ, accountID); err != nil {
			return err
		}
	}
	if err := seedGameAccounts(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_state(id,enabled,revision,changed_at) VALUES(1,1,1,0)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO donation_quota_capacity(id,rows_used,rows_held) VALUES(1,0,0)`); err != nil {
		return err
	}
	if err := seedProgressionState(ctx, tx, time.Now().Unix()); err != nil {
		return err
	}
	if err := seedGovernanceState(ctx, tx, time.Now().Unix()); err != nil {
		return err
	}
	var interactionPresent bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='fatfish_capacity')`).Scan(&interactionPresent); err != nil {
		return err
	}
	if interactionPresent {
		return seedInteractionStorage(ctx, tx)
	}
	return nil
}

func createFreshGenerationTwo(ctx context.Context, path string, secrets secret.GenerationTwoContextCodec) (*Store, error) {
	check, err := captureSourceSet(ctx, path)
	if err != nil {
		return nil, err
	}
	if check.main != nil || check.journal != nil || check.wal != nil || check.shm != nil {
		return nil, appendStartupError(startupError(StartupSourceChanged), check.close())
	}
	if err := check.close(); err != nil {
		return nil, err
	}
	if beforeFreshExclusiveCreateHook != nil {
		beforeFreshExclusiveCreateHook()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	created, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, startupError(StartupInitialization)
	}
	createdInfo, statErr := created.Stat()
	closeErr := created.Close()
	if statErr != nil || closeErr != nil || !createdInfo.Mode().IsRegular() {
		if createdInfo != nil {
			_ = newFreshOwnership(path, createdInfo).cleanup()
		}
		return nil, startupError(StartupInitialization)
	}
	owned := newFreshOwnership(path, createdInfo)
	if owner, ok := ctx.Value(databaseOwnerKey{}).(*databaseOwner); ok {
		owner.record(createdInfo)
	}
	if err := requireFreshSidecarsAbsent(path); err != nil {
		_ = owned.cleanup()
		return nil, err
	}
	// O_EXCL establishes the fresh ownership claim before this open. The
	// subsequent SQLite pathname open is intentionally the low-cost DEC-009
	// boundary: it is not a custom-VFS descriptor binding and does not claim to
	// resist a trusted local replacement between the two operations.
	if err := secureDBFiles(path); err != nil {
		return nil, appendStartupError(startupError(StartupUnsafePath), owned.cleanup())
	}
	RecordStartupStage(ctx, StageRawHandlesClosed)
	RecordStartupStage(ctx, StageSourceOpen)
	d, err := openSQLiteContext(ctx, path, "rw")
	if err != nil {
		return nil, appendStartupError(startupSQLFailure(ctx, err, StartupInitialization), owned.cleanup())
	}
	opened := sourceStore(ctx, d, secrets)
	committed := false
	failError := func(cause error) (*Store, error) {
		primary := preserveStartupCategory(cause, StartupInitialization)
		if !committed {
			opened.afterClose = func() error {
				return appendStartupError(owned.capturePresent(), owned.cleanup())
			}
		}
		// The close worker keeps ownership through rollback and cleanup. A
		// committed fresh database is retained for resumable recovery.
		return nil, appendStartupError(primary, opened.CloseContext(ctx))
	}
	fail := func(kind StartupErrorKind) (*Store, error) {
		return failError(startupContextCause(ctx, startupError(kind)))
	}
	if _, err := d.ExecContext(ctx, `PRAGMA foreign_keys=ON;`); err != nil {
		return fail(StartupInitialization)
	}
	if err := owned.capturePresent(); err != nil {
		return fail(StartupCleanupFailure)
	}
	RecordStartupStage(ctx, StageSchemaUpgrade)
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fail(StartupInitialization)
	}
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, generationTwoSchema); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if freshSchemaFailureHook != nil {
		if err := freshSchemaFailureHook(); err != nil {
			_ = tx.Rollback()
			return fail(StartupInitialization)
		}
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA application_id=0x4E425249; PRAGMA user_version=2;`); err != nil {
		_ = tx.Rollback()
		return fail(StartupInitialization)
	}
	announcementEpoch, err := GenerateOpaqueID("b1e_")
	if err != nil {
		_ = tx.Rollback()
		return fail(StartupInitialization)
	}
	if err := seedGenerationTwo(ctx, tx, announcementEpoch); err != nil {
		_ = tx.Rollback()
		return fail(StartupInitialization)
	}
	if err := validateGenerationTwoManifest(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if err := validateGenerationTwoSeedManifest(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if err := validateGenerationTwoFreshSeedManifest(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if _, err := validateCurrentGenerationTwoSiteConfig(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if err := foreignKeyCheck(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if err := tx.Commit(); err != nil {
		return fail(StartupInitialization)
	}
	committed = true
	RecordStartupCheckpoint(ctx, StageSchemaUpgrade)
	var applicationID, userVersion uint32
	if err := d.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil || applicationID != DatabaseApplicationID {
		return fail(StartupInitialization)
	}
	if err := d.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil || userVersion != DatabaseUserVersion {
		return fail(StartupInitialization)
	}
	var journalMode string
	if err := d.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil || !strings.EqualFold(journalMode, "wal") {
		return fail(StartupInitialization)
	}
	if err := inspectActiveDBFiles(path); err != nil {
		return fail(StartupUnsafePath)
	}
	RecordStartupStage(ctx, StageDomainRecovery)
	if err := checkpointAndRecoverBeforeListener(ctx, d, secrets); err != nil {
		return failError(err)
	}
	return opened, nil
}

// GenerationTwoSchemaHash is the externally reportable lock for the exact DDL
// source. The manifest builder independently checks the pinned value.
func GenerationTwoSchemaHash() string {
	sum := sha256.Sum256([]byte(generationTwoSchema))
	return hex.EncodeToString(sum[:])
}
