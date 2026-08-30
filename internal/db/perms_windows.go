//go:build windows

package db

import (
	"fmt"
	"os"
)

// Windows uses ACLs rather than POSIX owner/mode bits. The beta.1 target does
// not claim ACL equivalence, but it still performs a static Lstat/reparse
// check so a junction, symlink, special file, or non-regular sidecar cannot
// silently be accepted by this development build.
func secureDBParentDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect database directory: %w", err)
	}
	return validateDBParentPath(dir, info)
}

func secureDBFiles(path string) error {
	for _, item := range []struct {
		path string
		role string
	}{
		{path: path, role: "database file"},
		{path: path + "-wal", role: "wal file"},
		{path: path + "-shm", role: "shm file"},
	} {
		if err := requireRegularDBPath(item.path, item.role); err != nil {
			return err
		}
	}
	return nil
}
