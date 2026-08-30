//go:build linux && amd64

package db

import "os"

func validateDBPathPlatform(_ string, _ os.FileInfo) error { return nil }
