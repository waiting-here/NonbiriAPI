package db

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	DefaultStartupTimeout  = 300 * time.Second
	DefaultShutdownTimeout = 30 * time.Second
)

var ErrDatabaseInUse = errors.New("database is already owned by this process")

type CloseDeadlineError struct {
	Cause           error
	OpenConnections int
}

func (e *CloseDeadlineError) Error() string { return "database cleanup deadline exceeded" }
func (e *CloseDeadlineError) Unwrap() error { return e.Cause }

type databaseOwnerKey struct{}

type databaseOwner struct {
	path   string
	info   os.FileInfo
	active bool
}

func (o *databaseOwner) activate() {
	if o == nil {
		return
	}
	databaseOwners.Lock()
	o.active = true
	databaseOwners.Unlock()
}

func (o *databaseOwner) releasePreflight() {
	if o == nil {
		return
	}
	databaseOwners.Lock()
	if !o.active && databaseOwners.entries[o.path] == o {
		delete(databaseOwners.entries, o.path)
	}
	databaseOwners.Unlock()
}

var databaseOwners = struct {
	sync.Mutex
	entries map[string]*databaseOwner
}{entries: make(map[string]*databaseOwner)}

// Only metadata operations are allowed here. Opening an auxiliary descriptor
// could cancel another Store's POSIX locks when that descriptor is closed.
func acquireDatabaseOwner(path string) (*databaseOwner, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, startupError(StartupUnsafePath)
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	databaseOwners.Lock()
	defer databaseOwners.Unlock()
	if _, exists := databaseOwners.entries[abs]; exists {
		return nil, ErrDatabaseInUse
	}
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, startupError(StartupUnsafePath)
	}
	if info != nil {
		for _, existing := range databaseOwners.entries {
			existingInfo := existing.info
			if existingInfo == nil {
				// A fresh owner may have completed its exclusive create but
				// not yet recorded the new identity. Inspect the registered
				// name without opening a source descriptor in that interval.
				existingInfo, _ = os.Lstat(existing.path)
			}
			if existingInfo != nil && os.SameFile(info, existingInfo) {
				return nil, ErrDatabaseInUse
			}
		}
	}
	owner := &databaseOwner{path: abs, info: info}
	databaseOwners.entries[abs] = owner
	return owner, nil
}

func (o *databaseOwner) record(info os.FileInfo) {
	if o == nil {
		return
	}
	databaseOwners.Lock()
	o.info = info
	databaseOwners.Unlock()
}

func (o *databaseOwner) release() {
	if o == nil {
		return
	}
	databaseOwners.Lock()
	if databaseOwners.entries[o.path] == o {
		delete(databaseOwners.entries, o.path)
	}
	databaseOwners.Unlock()
}
