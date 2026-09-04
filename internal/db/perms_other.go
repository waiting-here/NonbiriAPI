//go:build !unix && !windows

package db

import "errors"

// The beta.1 database security contract is implemented only for the intended
// Linux/amd64 target. An unsupported OS has no equivalent permission check;
// fail closed before a writable SQLite handle can be returned.
func secureDBParentDir(string) error {
	return errors.New("database path security is unsupported on this target")
}

func secureDBFiles(string) error {
	return errors.New("database file security is unsupported on this target")
}
