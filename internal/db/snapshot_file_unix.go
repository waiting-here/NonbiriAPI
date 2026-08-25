//go:build unix

package db

import (
	"fmt"
	"os"
	"syscall"
)

func openReadOnlyNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

func validateSourceFile(_ *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unsupported file identity")
	}
	if stat.Uid != uint32(syscall.Geteuid()) || stat.Nlink != 1 || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("database file ownership, links, or mode is unsafe")
	}
	return nil
}
