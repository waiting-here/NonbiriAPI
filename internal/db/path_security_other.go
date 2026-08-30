//go:build !unix && !windows

package db

import "os"

// Unsupported targets intentionally provide no path-security equivalence.
// secureDBParentDir and openReadOnlyNoFollow fail closed there; this hook is
// only needed so the common lexical walk can compile on those targets.
func validateDBPathPlatform(_ string, _ os.FileInfo) error { return nil }
