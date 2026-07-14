//go:build windows

package machinecatalog

import (
	"os"
	"syscall"
)

func isReparsePoint(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}
