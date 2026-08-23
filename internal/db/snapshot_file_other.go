//go:build !unix && !windows

package db

import "os"

func openReadOnlyNoFollow(path string) (*os.File, error) { return os.Open(path) }

func validateSourceFile(_ *os.File, _ os.FileInfo) error { return nil }
