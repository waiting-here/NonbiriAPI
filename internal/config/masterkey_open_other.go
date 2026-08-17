//go:build !unix

package config

import "os"

// Non-Unix targets do not expose a portable O_NOFOLLOW through os.FileMode.
// The caller still rejects a static symlink and binds the opened descriptor to
// the Lstat identity. Production deployment targets use the Unix opener.
func openMasterKeyFilePlatform(path string) (*os.File, error) {
	return os.Open(path)
}
