//go:build !unix && !windows

package db

import (
	"errors"
	"os"
)

func openReadOnlyNoFollow(string) (*os.File, error) {
	return nil, errors.New("database no-follow open is unsupported on this target")
}

func validateSourceFile(_ *os.File, _ os.FileInfo) error {
	return errors.New("database source validation is unsupported on this target")
}
