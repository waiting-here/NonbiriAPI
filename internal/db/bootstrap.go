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
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	DatabaseApplicationID uint32 = 0x4E425249
	DatabaseUserVersion   uint32 = 1
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
)

var ErrDatabaseStartup = errors.New("database startup rejected")

// StartupError intentionally carries no path, SQL, endpoint, row identifier,
// ciphertext, or cryptographic detail. Generation is safe to expose only for
// the marker mismatch category.
type StartupError struct {
	Kind               StartupErrorKind
	ExpectedGeneration uint32
	ActualGeneration   uint32
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

func (s *sourceSnapshotSet) close() {
	if s == nil {
		return
	}
	for _, item := range []*sourceFileSnapshot{s.main, s.journal, s.wal, s.shm} {
		if item != nil && item.file != nil {
			_ = item.file.Close()
			item.file = nil
		}
	}
}

func captureSourceSet(path string) (*sourceSnapshotSet, error) {
	set := &sourceSnapshotSet{}
	var err error
	if set.main, err = captureSourceFile(path, "database file"); err != nil {
		set.close()
		return nil, err
	}
	if set.journal, err = captureSourceFile(path+"-journal", "rollback journal file"); err != nil {
		set.close()
		return nil, err
	}
	if set.wal, err = captureSourceFile(path+"-wal", "wal file"); err != nil {
		set.close()
		return nil, err
	}
	if set.shm, err = captureSourceFile(path+"-shm", "shm file"); err != nil {
		set.close()
		return nil, err
	}
	return set, nil
}

func captureSourceFile(path, role string) (*sourceFileSnapshot, error) {
	lstat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return nil, startupError(StartupUnsafePath)
	}
	f, err := openReadOnlyNoFollow(path)
	if err != nil {
		return nil, startupError(StartupUnsafePath)
	}
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			_ = f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(lstat, info) {
		return nil, startupError(StartupUnsafePath)
	}
	if err := validateSourceFile(f, info); err != nil {
		return nil, startupError(StartupUnsafePath)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, startupError(StartupSourceChanged)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, startupError(StartupSourceChanged)
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

func recheckSourceSet(path string, original *sourceSnapshotSet) error {
	current, err := captureSourceSet(path)
	if err != nil {
		return startupError(StartupSourceChanged)
	}
	defer current.close()
	if !equalSourceFile(original.main, current.main) ||
		!equalSourceFile(original.journal, current.journal) ||
		!equalSourceFile(original.wal, current.wal) ||
		!equalSourceFile(original.shm, current.shm) {
		return startupError(StartupSourceChanged)
	}
	return nil
}

func openGenerationOne(path string, secrets secret.Codec) (*Store, error) {
	initial, err := captureSourceSet(path)
	if err != nil {
		return nil, err
	}
	// A rollback journal may contain uncommitted dirty pages whose recovery
	// would write the source database. Fresh-only startup never opens, copies,
	// recovers, or deletes it, regardless of whether SQLite would call it hot.
	if initial.journal != nil {
		initial.close()
		return nil, startupError(StartupRollbackJournal)
	}
	if initial.main == nil {
		fresh := initial.journal == nil && initial.wal == nil && initial.shm == nil
		initial.close()
		if !fresh {
			return nil, startupError(StartupIncompleteFresh)
		}
		return createFreshGenerationOne(path, secrets)
	}
	defer initial.close()
	if err := validateHeader(initial.main); err != nil {
		return nil, err
	}
	if err := validateCurrentSnapshot(path, initial, secrets); err != nil {
		return nil, err
	}
	return openValidatedSource(path, initial, secrets)
}

func validateHeader(main *sourceFileSnapshot) error {
	if main == nil || main.size < 100 {
		return startupError(StartupInvalidHeader)
	}
	var header [100]byte
	n, err := main.file.ReadAt(header[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return startupError(StartupInvalidHeader)
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

func copySnapshot(source *sourceFileSnapshot, destination string) error {
	if source == nil || source.file == nil {
		return startupError(StartupSourceChanged)
	}
	if _, err := source.file.Seek(0, io.SeekStart); err != nil {
		return startupError(StartupSourceChanged)
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return startupError(StartupInitialization)
	}
	createdInfo, err := out.Stat()
	if err != nil {
		_ = out.Close()
		return startupError(StartupInitialization)
	}
	copyOK := false
	defer func() {
		if !copyOK {
			_ = out.Close()
			if current, err := os.Lstat(destination); err == nil && current.Mode().IsRegular() &&
				current.Mode()&os.ModeSymlink == 0 && os.SameFile(createdInfo, current) {
				_ = os.Remove(destination)
			}
		}
	}()
	written, err := io.Copy(out, source.file)
	if err != nil || written != source.size {
		return startupError(StartupSourceChanged)
	}
	if err := out.Sync(); err != nil {
		return startupError(StartupSourceChanged)
	}
	if err := out.Close(); err != nil {
		return startupError(StartupSourceChanged)
	}
	copied, err := os.Open(destination)
	if err != nil {
		return startupError(StartupInitialization)
	}
	h := sha256.New()
	_, hashErr := io.Copy(h, copied)
	closeErr := copied.Close()
	if hashErr != nil || closeErr != nil || !equalDigest(h.Sum(nil), source.digest) {
		return startupError(StartupSourceChanged)
	}
	copyOK = true
	return nil
}

func equalDigest(sum []byte, expected [sha256.Size]byte) bool {
	return len(sum) == len(expected) && string(sum) == string(expected[:])
}

func validateCurrentSnapshot(path string, source *sourceSnapshotSet, secrets secret.Codec) error {
	workspace, err := newValidationWorkspace()
	if err != nil {
		return err
	}
	var validationErr error
	if err := copySnapshot(source.main, workspace.mainPath); err != nil {
		validationErr = err
	} else if err := workspace.recordOwned(workspace.mainPath); err != nil {
		validationErr = err
	} else if source.wal != nil {
		validationErr = copySnapshot(source.wal, workspace.mainPath+"-wal")
		if validationErr == nil {
			validationErr = workspace.recordOwned(workspace.mainPath + "-wal")
		}
	}
	if validationErr == nil {
		if afterSnapshotCopyHook != nil {
			afterSnapshotCopyHook()
		}
		validationErr = recheckSourceSet(path, source)
	}
	if validationErr == nil {
		validationErr = validateReadOnlyCopy(workspace, secrets)
	}
	// validateReadOnlyCopy closes SQLite before returning. The final source
	// comparison therefore covers the complete copy validation interval.
	if finalErr := recheckSourceSet(path, source); finalErr != nil {
		validationErr = finalErr
	}
	if cleanupErr := workspace.cleanup(); cleanupErr != nil {
		return cleanupErr
	}
	return validationErr
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

func validateReadOnlyCopy(workspace *validationWorkspace, secrets secret.Codec) (result error) {
	d, err := openSQLite(workspace.mainPath, "ro")
	if err != nil {
		return startupError(StartupCorruptDatabase)
	}
	defer func() {
		if err := d.Close(); err != nil {
			result = startupError(StartupCorruptDatabase)
		}
		if err := workspace.recordSQLiteSidecars(); err != nil {
			result = err
		}
	}()
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `PRAGMA query_only=ON; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if err := workspace.recordSQLiteSidecars(); err != nil {
		return err
	}
	// Page one can itself be committed in the WAL. The raw source header is the
	// pre-SQLite gate; these PRAGMAs verify the merged main+WAL view.
	var applicationID, userVersion uint32
	if err := d.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if applicationID != DatabaseApplicationID {
		return startupError(StartupWrongIdentity)
	}
	if err := d.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if userVersion != DatabaseUserVersion {
		return generationError(userVersion)
	}
	if err := quickCheck(ctx, d); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if err := foreignKeyCheck(ctx, d); err != nil {
		return startupError(StartupCorruptDatabase)
	}
	if err := validateGenerationOneManifest(ctx, d); err != nil {
		return startupError(StartupSchemaMismatch)
	}
	if err := validateEndpointKeyEnvelopes(ctx, d, secrets); err != nil {
		return startupError(StartupCredentialReject)
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

func validateEndpointKeyEnvelopes(ctx context.Context, q queryer, codec secret.Codec) error {
	rows, err := q.QueryContext(ctx, `
SELECT ek.id,
       e.id,
       u.id,
       typeof(e.base_url),
       length(CAST(e.base_url AS BLOB)),
       CASE
         WHEN typeof(e.base_url)='text'
          AND length(CAST(e.base_url AS BLOB)) BETWEEN 1 AND ?
         THEN e.base_url
       END,
       typeof(ek.encrypted_secret),
       length(CAST(ek.encrypted_secret AS BLOB)),
       CASE
         WHEN typeof(ek.encrypted_secret)='text'
          AND length(CAST(ek.encrypted_secret AS BLOB)) BETWEEN 1 AND ?
         THEN ek.encrypted_secret
       END
FROM endpoint_keys ek
JOIN endpoints e ON e.id=ek.endpoint_id
JOIN users u ON u.id=e.user_id
ORDER BY ek.id`, maxStoredEndpointBaseURLBytes, maxEndpointCredentialEnvelopeBytes)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var keyID, endpointID, userID int64
		var baseType, ciphertextType string
		var baseBytes, ciphertextBytes int64
		var baseURL, ciphertext sql.NullString
		if err := rows.Scan(
			&keyID,
			&endpointID,
			&userID,
			&baseType,
			&baseBytes,
			&baseURL,
			&ciphertextType,
			&ciphertextBytes,
			&ciphertext,
		); err != nil ||
			keyID <= 0 || endpointID <= 0 || userID <= 0 ||
			baseType != "text" || baseBytes < 1 || baseBytes > maxStoredEndpointBaseURLBytes ||
			!baseURL.Valid || int64(len(baseURL.String)) != baseBytes || !utf8.ValidString(baseURL.String) ||
			ciphertextType != "text" || ciphertextBytes < 1 || ciphertextBytes > maxEndpointCredentialEnvelopeBytes ||
			!ciphertext.Valid || int64(len(ciphertext.String)) != ciphertextBytes || !utf8.ValidString(ciphertext.String) {
			baseURL.String, ciphertext.String = "", ""
			return errors.New("invalid endpoint credential row")
		}
		_, origin, err := egress.CanonicalEndpointTarget(baseURL.String)
		baseURL.String = ""
		if err != nil {
			ciphertext.String = ""
			return errors.New("invalid endpoint credential origin")
		}
		credentialContext, err := secret.NewEndpointKeyContext(userID, endpointID, keyID, origin)
		origin = ""
		if err != nil {
			ciphertext.String = ""
			return errors.New("invalid endpoint credential context")
		}
		version, err := secret.ParseEnvelopeVersion(ciphertext.String)
		if err != nil || version != secret.EnvelopeVersionV2 {
			ciphertext.String = ""
			return errors.New("invalid endpoint credential envelope")
		}
		plaintext, err := codec.OpenForContext(ciphertext.String, credentialContext)
		ciphertext.String = ""
		clear(plaintext)
		if err != nil {
			return errors.New("invalid endpoint credential authentication")
		}
	}
	return rows.Err()
}

var beforeWritableOpenHook func()
var afterSnapshotCopyHook func()
var freshSchemaFailureHook func() error

func openValidatedSource(path string, expected *sourceSnapshotSet, secrets secret.Codec) (*Store, error) {
	if beforeWritableOpenHook != nil {
		beforeWritableOpenHook()
	}
	// Keep the original no-follow handles alive and capture one last complete
	// set immediately before sql.Open. On Windows those handles also deny path
	// replacement; on Unix the owner-only parent gate excludes other users.
	if err := recheckSourceSet(path, expected); err != nil {
		return nil, err
	}
	d, err := openSQLite(path, "rw")
	if err != nil {
		return nil, startupError(StartupInitialization)
	}
	fail := func() (*Store, error) {
		_ = d.Close()
		return nil, startupError(StartupInitialization)
	}
	if _, err := d.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fail()
	}
	var journalMode string
	if err := d.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil || !strings.EqualFold(journalMode, "wal") {
		return fail()
	}
	if err := secureDBFiles(path); err != nil {
		return fail()
	}
	return &Store{db: d, secrets: secrets}, nil
}

type freshOwnership struct {
	path  string
	files map[string]ownedFileEvidence
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
		o.files[name] = makeOwnedFileEvidence(info)
	}
	return nil
}

func (o *freshOwnership) cleanup() error {
	if o == nil || o.path == "" {
		return startupError(StartupCleanupFailure)
	}
	for _, candidate := range []string{o.path + "-shm", o.path + "-wal", o.path + "-journal", o.path} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		owned, ok := o.files[filepath.Base(candidate)]
		if err != nil || !ok || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !owned.matches(info) {
			return startupError(StartupCleanupFailure)
		}
		if err := os.Remove(candidate); err != nil {
			return startupError(StartupCleanupFailure)
		}
	}
	return nil
}

func createFreshGenerationOne(path string, secrets secret.Codec) (*Store, error) {
	check, err := captureSourceSet(path)
	if err != nil {
		return nil, err
	}
	if check.main != nil || check.journal != nil || check.wal != nil || check.shm != nil {
		check.close()
		return nil, startupError(StartupSourceChanged)
	}
	check.close()
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
	d, err := openSQLite(path, "rw")
	if err != nil {
		_ = owned.cleanup()
		return nil, startupError(StartupInitialization)
	}
	fail := func(kind StartupErrorKind) (*Store, error) {
		captureErr := owned.capturePresent()
		_ = d.Close()
		if captureErr != nil {
			return nil, captureErr
		}
		if cleanupErr := owned.cleanup(); cleanupErr != nil {
			return nil, cleanupErr
		}
		return nil, startupError(kind)
	}
	if _, err := d.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		return fail(StartupInitialization)
	}
	if err := owned.capturePresent(); err != nil {
		return fail(StartupCleanupFailure)
	}
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fail(StartupInitialization)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, generationOneSchema); err != nil {
		_ = tx.Rollback()
		return fail(StartupSchemaMismatch)
	}
	if freshSchemaFailureHook != nil {
		if err := freshSchemaFailureHook(); err != nil {
			_ = tx.Rollback()
			return fail(StartupInitialization)
		}
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA application_id=0x4E425249; PRAGMA user_version=1;`); err != nil {
		_ = tx.Rollback()
		return fail(StartupInitialization)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO site_config(key,value,updated_at) VALUES
('maintenance_mode','1',0),
('registration_open','0',0),
('games_enabled','0',0),
('game_fishing_enabled','0',0)`); err != nil {
		_ = tx.Rollback()
		return fail(StartupInitialization)
	}
	if err := validateGenerationOneManifest(ctx, tx); err != nil {
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
	var applicationID, userVersion uint32
	if err := d.QueryRow(`PRAGMA application_id`).Scan(&applicationID); err != nil || applicationID != DatabaseApplicationID {
		return fail(StartupInitialization)
	}
	if err := d.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil || userVersion != DatabaseUserVersion {
		return fail(StartupInitialization)
	}
	var journalMode string
	if err := d.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&journalMode); err != nil || !strings.EqualFold(journalMode, "wal") {
		return fail(StartupInitialization)
	}
	if err := secureDBFiles(path); err != nil {
		return fail(StartupUnsafePath)
	}
	if err := validateEndpointKeyEnvelopes(ctx, d, secrets); err != nil {
		return fail(StartupCredentialReject)
	}
	return &Store{db: d, secrets: secrets}, nil
}

// GenerationOneSchemaHash is the externally reportable lock for the exact DDL
// source. The manifest builder independently checks the pinned value.
func GenerationOneSchemaHash() string {
	sum := sha256.Sum256([]byte(generationOneSchema))
	return hex.EncodeToString(sum[:])
}
