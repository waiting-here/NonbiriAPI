//go:build unix && (!linux || !amd64)

package db

import "errors"

// The release gate promises the hardened database path only on Linux/amd64.
// Other Unix targets must not inherit a partially equivalent implementation
// and then continue to a writable SQLite open.
func secureDBParentDir(string) error {
	return errors.New("database path security is unsupported on this target")
}

func secureDBFiles(string) error {
	return errors.New("database file security is unsupported on this target")
}
