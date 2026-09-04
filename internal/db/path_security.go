package db

import (
	"fmt"
	"os"
)

// validateDBParentPath rejects path components that would make a database
// directory resolve somewhere other than the configured lexical path. The
// platform hook adds Windows reparse-point detection; POSIX mode and owner
// checks remain the responsibility of secureDBParentDir.
func validateDBParentPath(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("database directory component must not be a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("database directory component is not a directory")
	}
	return validateDBPathPlatform(path, info)
}

func validateDBRegularPath(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path must be a regular file")
	}
	return validateDBPathPlatform(path, info)
}
