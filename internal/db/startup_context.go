package db

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func sourceStore(ctx context.Context, d *sql.DB, secrets secret.GenerationTwoContextCodec) *Store {
	owner, _ := ctx.Value(databaseOwnerKey{}).(*databaseOwner)
	owner.activate()
	return &Store{db: d, secrets: secrets, owner: owner}
}

func startupContextCause(ctx context.Context, cause error) error {
	if cause == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return cause
}

func startupSQLFailure(ctx context.Context, err error, fallback StartupErrorKind) error {
	return preserveStartupCategory(startupContextCause(ctx, err), fallback)
}

// startupFailures keeps the primary error authoritative and exposes only
// bounded, safe categories for secondary cleanup or source-check failures.
type startupFailures struct {
	primary   error
	secondary []string
}

func (e *startupFailures) Error() string { return e.primary.Error() }
func (e *startupFailures) Unwrap() error { return e.primary }
func (e *startupFailures) SecondaryCategories() []string {
	return append([]string(nil), e.secondary...)
}

func appendStartupError(primary, secondary error) error {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	result, ok := primary.(*startupFailures)
	if !ok {
		result = &startupFailures{primary: primary}
	}
	if len(result.secondary) < 8 {
		category := "cleanup_failed"
		var classified *StartupError
		if errors.As(secondary, &classified) {
			category = string(classified.Kind)
		}
		if errors.Is(secondary, context.DeadlineExceeded) {
			category = "cleanup_deadline"
		}
		if errors.Is(secondary, context.Canceled) {
			category = "cancelled"
		}
		result.secondary = append(result.secondary, category)
	}
	return result
}

// WithStartupCleanupError retains the primary cause and appends only a safe,
// bounded cleanup category for the process composition root.
func WithStartupCleanupError(primary, secondary error) error {
	return appendStartupError(primary, secondary)
}

func startupIOError(ctx context.Context, _ error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return startupError(StartupReadIO)
}

func sourceChangeReason(a, b *sourceFileSnapshot) string {
	if a == nil || b == nil {
		if a != b {
			return "member"
		}
		return ""
	}
	if !os.SameFile(a.info, b.info) {
		return "identity"
	}
	if a.size != b.size {
		return "size"
	}
	if a.mtime != b.mtime {
		return "mtime"
	}
	if a.mode != b.mode {
		return "identity"
	}
	if a.digest != b.digest {
		return "digest"
	}
	return ""
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			written, err := dst.Write(buffer[:n])
			total += int64(written)
			recordStartupProgress(ctx, 0, int64(written))
			if err != nil {
				return total, err
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func openSQLiteContext(ctx context.Context, path, mode string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dsn, err := sqliteFileURI(path, mode)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	budget := 5000
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline).Milliseconds()
		if remaining < 0 {
			return nil, context.DeadlineExceeded
		}
		if remaining < int64(budget) {
			budget = int(remaining)
		}
	}
	q := u.Query()
	q.Del("_pragma")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout("+strconv.Itoa(budget)+")")
	u.RawQuery = q.Encode()
	d, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	return d, nil
}
