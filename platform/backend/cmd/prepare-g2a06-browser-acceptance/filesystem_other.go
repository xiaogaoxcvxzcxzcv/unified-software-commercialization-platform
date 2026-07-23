//go:build !windows

package main

import (
	"errors"
	"os"
)

type runtimeDirectoryGuard struct{ path string }
type runtimeFileIdentity struct{ info os.FileInfo }

func lockRuntimeDirectory(path string) (*runtimeDirectoryGuard, error) {
	return nil, errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func (g *runtimeDirectoryGuard) Verify() error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func (g *runtimeDirectoryGuard) Close() error { return nil }

func validateRuntimeFile(path string) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func verifyRuntimeFileSecurity(path string) error { return validateRuntimeFile(path) }

func openNewRuntimeFile(path string) (*os.File, error) {
	return nil, errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func openReadRuntimeFile(path string) (*os.File, error) {
	return nil, errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func openRecoveryRuntimeFile(path string) (*os.File, error) {
	return nil, errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func validateOpenRuntimeFile(file *os.File) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func openRuntimeFileIdentity(file *os.File) (runtimeFileIdentity, error) {
	return runtimeFileIdentity{}, errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func sameRuntimeFileIdentity(left, right runtimeFileIdentity) bool {
	return false
}
func validateRuntimeFileIdentity(path string, expected runtimeFileIdentity) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func validateOpenRuntimeFilePath(file *os.File, path string, expected runtimeFileIdentity) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func removeRuntimeFile(path string) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func removeRuntimeFileWithHook(path string, hook func(string)) error {
	return removeRuntimeFileWithHooks(path, hook, nil)
}
func removeRuntimeFileWithHooks(path string, beforeDisposition, afterDisposition func(string)) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
func removeOpenRuntimeFile(file *os.File, path string, expected runtimeFileIdentity) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func secureRuntimePath(path string, directory bool) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func replaceOpenRuntimeFile(_ *os.File, source, destination string, _ runtimeFileIdentity, hook func(string), postcheck func() error, cleanupOnFailure bool) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}

func syncDirectory(path string) error {
	return errors.New("G2A-06 browser acceptance filesystem is supported only on Windows")
}
