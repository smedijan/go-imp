package ledger

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// DirectoryMetadata queries current directory attributes through a no-follow
// handle. Windows DirEntry.Info retains enumeration metadata, which may be stale
// on NTFS; GetFileInformationByHandle (File.Stat) reads current attributes.
func DirectoryMetadata(path string, _ fs.DirEntry) (fs.FileInfo, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Preserve the long-path support provided by Go's ordinary filesystem APIs.
	extended := absolute
	if !strings.HasPrefix(extended, `\\?\`) {
		if strings.HasPrefix(extended, `\\`) {
			extended = `\\?\UNC\` + extended[2:]
		} else {
			extended = `\\?\` + extended
		}
	}
	name, err := windows.UTF16PtrFromString(extended)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	directory := os.NewFile(uintptr(handle), path)
	if directory == nil {
		_ = windows.CloseHandle(handle)

		return nil, fmt.Errorf("directory metadata handle unavailable")
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Stat()
	if err != nil {
		return nil, err
	}
	// A stale enumeration entry may have been replaced with a junction/symlink.
	// Never follow it or accept any other nonregular directory reparse point.
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if info.Mode().Type() != fs.ModeDir || !ok || attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf("unsupported directory metadata type")
	}

	return info, nil
}
