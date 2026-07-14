//go:build !windows

package generation

import "os"

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
