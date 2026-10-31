//go:build !windows

package ledger

import "io/fs"

// DirectoryMetadata returns the metadata used to fingerprint a directory.
func DirectoryMetadata(_ string, entry fs.DirEntry) (fs.FileInfo, error) {
	return entry.Info()
}
