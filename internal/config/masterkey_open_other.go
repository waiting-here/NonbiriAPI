//go:build !unix

package config

import "os"

// Non-Unix targets do not expose a portable O_NOFOLLOW through os.FileMode.
// The caller still rejects a static symlink and binds the opened descriptor to
// the Lstat identity. Production deployment targets use the Unix opener.
func openMasterKeyFilePlatform(path string) (*os.File, error) {
	// #nosec G304 -- the operator deliberately selects this startup key path;
	// the caller Lstats it, binds the opened descriptor with os.SameFile, requires
	// a regular file, rechecks identity/metadata after a bounded read, and never
	// uses the bytes as another pathname.
	return os.Open(path)
}
