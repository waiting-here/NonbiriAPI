//go:build windows

package db

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// validateDBPathPlatform performs the Windows portion of the beta.1 static
// path gate. FILE_ATTRIBUTE_REPARSE_POINT covers junctions and other reparse
// points which os.FileInfo.Mode does not consistently expose as ModeSymlink.
// This is intentionally a static development-time check, not an ACL or
// same-UID race guarantee.
func validateDBPathPlatform(path string, _ os.FileInfo) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode path %q for reparse check: %w", path, err)
	}
	// GetFileAttributesEx does not require directory-list/read access to the
	// component (unlike a FILE_FLAG_OPEN_REPARSE_POINT handle), while still
	// exposing the component's FILE_ATTRIBUTE_REPARSE_POINT bit. Final
	// symlinks are separately rejected by the common Lstat check.
	var info windows.Win32FileAttributeData
	if err := windows.GetFileAttributesEx(name, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&info))); err != nil {
		return fmt.Errorf("read path %q reparse attributes: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("path must not be a reparse point")
	}
	return nil
}
