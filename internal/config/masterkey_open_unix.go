//go:build unix

package config

import (
	"os"
	"syscall"
)

// openMasterKeyFilePlatform atomically refuses a final-component symlink.
// O_NONBLOCK prevents a pathname swap to a FIFO from stalling startup before
// the descriptor's regular-file identity can be checked.
func openMasterKeyFilePlatform(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
