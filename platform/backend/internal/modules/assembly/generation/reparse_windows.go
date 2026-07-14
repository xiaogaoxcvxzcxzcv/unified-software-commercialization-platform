//go:build windows

package generation

import (
	"os"
	"syscall"
)

func isUnsafeFilesystemEntry(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}
