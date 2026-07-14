package machinecontract

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

var ErrUnsafeRelativePath = errors.New("unsafe generated relative path")

func ValidateSafeRelativePath(value string) error {
	if value == "" || len(value) > 1024 || strings.ContainsRune(value, 0) || strings.Contains(value, "\\") {
		return ErrUnsafeRelativePath
	}
	if strings.HasPrefix(value, "/") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || path.Clean(value) != value {
		return ErrUnsafeRelativePath
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, ":") || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") || reservedWindowsName(segment) {
			return ErrUnsafeRelativePath
		}
	}
	return nil
}

func reservedWindowsName(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}
