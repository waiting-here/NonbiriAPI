//go:build !unix

package db

// secureDBParentDir is a no-op on non-Unix targets. Windows file security is
// ACL-based; the application does not synthesize POSIX permission bits and does
// not claim an automatic owner-only guarantee. Deployment documentation
// requires a dedicated service account and an ACL that grants access only to
// that account and administrators.
func secureDBParentDir(dir string) error { return nil }

// secureDBFiles is a no-op on non-Unix targets for the same reason: Windows
// ACLs are the authoritative access-control mechanism and are configured by
// the operator, not inferred from POSIX mode bits. The shared path-shape check
// (rejecting a symlink or non-regular file at the database path) still applies.
func secureDBFiles(path string) error { return nil }
