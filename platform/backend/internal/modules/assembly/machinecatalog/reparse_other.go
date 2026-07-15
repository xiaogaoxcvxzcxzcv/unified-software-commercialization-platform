//go:build !windows

package machinecatalog

func isReparsePoint(string) (bool, error) {
	return false, nil
}
