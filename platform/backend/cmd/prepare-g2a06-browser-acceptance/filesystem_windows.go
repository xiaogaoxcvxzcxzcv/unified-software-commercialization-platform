//go:build windows

package main

import (
	"encoding/csv"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	moveFileExProc         = kernel32.NewProc("MoveFileExW")
	createFileProc         = kernel32.NewProc("CreateFileW")
	getFileInfoProc        = kernel32.NewProc("GetFileInformationByHandle")
	setFileInfoProc        = kernel32.NewProc("SetFileInformationByHandle")
	flushFileBuffersProc   = kernel32.NewProc("FlushFileBuffers")
	getFinalPathNameProc   = kernel32.NewProc("GetFinalPathNameByHandleW")
	resolveWindowsOwnerSID = windowsOwnerSID
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
	fileReadAttributes      = 0x80
	fileListDirectory       = 0x1
	fileAddFile             = 0x2
	synchronizeAccess       = 0x00100000
	readControl             = 0x00020000
	deleteAccess            = 0x00010000
	genericRead             = 0x80000000
	genericWrite            = 0x40000000
	fileShareRead           = 0x1
	fileShareWrite          = 0x2
	openExisting            = 3
	createNew               = 1
	fileFlagBackupSemantics = 0x02000000
	fileFlagOpenReparse     = 0x00200000
	fileAttributeDirectory  = 0x10
	fileAttributeReparse    = 0x400
	fileRenameInfoClass     = 3
	fileDispositionClass    = 4
)

type windowsFileIdentity struct {
	volume, indexHigh, indexLow uint32
}

type runtimeFileIdentity struct{ value windowsFileIdentity }

type byHandleFileInformation struct {
	FileAttributes     uint32
	CreationTime       syscall.Filetime
	LastAccessTime     syscall.Filetime
	LastWriteTime      syscall.Filetime
	VolumeSerialNumber uint32
	FileSizeHigh       uint32
	FileSizeLow        uint32
	NumberOfLinks      uint32
	FileIndexHigh      uint32
	FileIndexLow       uint32
}

type runtimeDirectoryGuard struct {
	path     string
	handle   syscall.Handle
	identity windowsFileIdentity
}

func lockRuntimeDirectory(path string) (*runtimeDirectoryGuard, error) {
	handle, info, err := openWindowsObject(path, true)
	if err != nil {
		return nil, err
	}
	return &runtimeDirectoryGuard{path: path, handle: handle, identity: fileIdentity(info)}, nil
}

func (g *runtimeDirectoryGuard) Verify() error {
	if g == nil || g.handle == syscall.InvalidHandle {
		return errors.New("controlled directory guard is closed")
	}
	current, err := windowsHandleInfo(g.handle)
	if err != nil || fileIdentity(current) != g.identity {
		return errors.New("controlled directory handle identity changed")
	}
	probe, info, err := openWindowsObject(g.path, true)
	if err != nil {
		return err
	}
	_ = syscall.CloseHandle(probe)
	if fileIdentity(info) != g.identity {
		return errors.New("controlled directory path identity changed")
	}
	return nil
}

func (g *runtimeDirectoryGuard) Close() error {
	if g == nil || g.handle == syscall.InvalidHandle {
		return nil
	}
	err := syscall.CloseHandle(g.handle)
	g.handle = syscall.InvalidHandle
	return err
}

func secureRuntimePath(path string, directory bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("controlled runtime path is unavailable or linked")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || !strings.EqualFold(filepath.Clean(abs), filepath.Clean(resolved)) {
		return errors.New("controlled runtime path contains reparse escape")
	}
	handle, initial, err := openWindowsObject(abs, directory)
	if err != nil || !directory && initial.NumberOfLinks != 1 {
		if handle != syscall.InvalidHandle {
			_ = syscall.CloseHandle(handle)
		}
		return errors.New("controlled runtime object identity is unsafe")
	}
	defer syscall.CloseHandle(handle)
	identity := fileIdentity(initial)
	sid, err := currentUserSID()
	if err != nil {
		return errors.New("resolve controlled runtime owner")
	}
	owner, err := resolveWindowsOwnerSID(abs)
	if err != nil {
		return errors.New("resolve controlled runtime owner")
	}
	if owner != sid && owner != "S-1-5-18" && owner != "S-1-5-32-544" {
		return errors.New("controlled runtime owner is not trusted")
	}
	if verifyWindowsACL(abs, sid) == nil {
		final, infoErr := windowsHandleInfo(handle)
		if infoErr == nil && fileIdentity(final) == identity && (directory || final.NumberOfLinks == 1) {
			return nil
		}
		return errors.New("controlled runtime object changed during ACL verification")
	}
	const aclScript = `if($env:G2A06_ACL_DIRECTORY -eq '1'){$acl=New-Object System.Security.AccessControl.DirectorySecurity;$inherit=[System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'}else{$acl=New-Object System.Security.AccessControl.FileSecurity;$inherit=[System.Security.AccessControl.InheritanceFlags]::None};$principal=New-Object System.Security.Principal.SecurityIdentifier($env:G2A06_ACL_SID);$acl.SetOwner($principal);$acl.SetAccessRuleProtection($true,$false);$prop=[System.Security.AccessControl.PropagationFlags]::None;$allow=[System.Security.AccessControl.AccessControlType]::Allow;$full=[System.Security.AccessControl.FileSystemRights]::FullControl;foreach($value in @($env:G2A06_ACL_SID,$env:G2A06_SYSTEM_SID,$env:G2A06_ADMIN_SID)){$principal=New-Object System.Security.Principal.SecurityIdentifier($value);$rule=New-Object System.Security.AccessControl.FileSystemAccessRule($principal,$full,$inherit,$prop,$allow);$acl.AddAccessRule($rule)|Out-Null};Set-Acl -LiteralPath $env:G2A06_ACL_PATH -AclObject $acl`
	command, commandErr := securityPowerShell(aclScript)
	if commandErr != nil {
		return errors.New("construct controlled runtime ACL")
	}
	directoryValue := "0"
	if directory {
		directoryValue = "1"
	}
	command.Env = append(os.Environ(), "G2A06_ACL_PATH="+abs, "G2A06_ACL_SID="+sid, "G2A06_SYSTEM_SID=S-1-5-18", "G2A06_ADMIN_SID=S-1-5-32-544", "G2A06_ACL_DIRECTORY="+directoryValue)
	if commandErr = command.Run(); commandErr != nil {
		return errors.New("protect controlled runtime ACL")
	}
	final, err := windowsHandleInfo(handle)
	if err != nil || fileIdentity(final) != identity || !directory && final.NumberOfLinks != 1 {
		return errors.New("controlled runtime object changed during ACL protection")
	}
	if err = verifyWindowsACL(abs, sid); err != nil {
		return err
	}
	return nil
}

func validateRuntimeFile(path string) error {
	handle, info, err := openWindowsObject(path, false)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	if info.NumberOfLinks != 1 {
		return errors.New("controlled file has multiple links")
	}
	return nil
}

func verifyRuntimeFileSecurity(path string) error {
	if err := validateRuntimeFile(path); err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	return verifyWindowsACL(path, sid)
}

func openNewRuntimeFile(path string) (*os.File, error) {
	handle, err := createWindowsFile(path, genericWrite|deleteAccess|fileReadAttributes|readControl, createNew)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, errors.New("create controlled file handle")
	}
	return file, nil
}

func openReadRuntimeFile(path string) (*os.File, error) {
	handle, err := createWindowsFile(path, genericRead|fileReadAttributes|readControl, openExisting)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, errors.New("open controlled file handle")
	}
	return file, nil
}

func openRecoveryRuntimeFile(path string) (*os.File, error) {
	handle, err := createWindowsFile(path, genericRead|deleteAccess|fileReadAttributes|readControl, openExisting)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, errors.New("open controlled recovery handle")
	}
	return file, nil
}

func validateOpenRuntimeFile(file *os.File) error {
	if file == nil {
		return errors.New("controlled file handle is unavailable")
	}
	info, err := windowsHandleInfo(syscall.Handle(file.Fd()))
	if err != nil || info.FileAttributes&(fileAttributeDirectory|fileAttributeReparse) != 0 || info.NumberOfLinks != 1 {
		return errors.New("controlled file handle identity is unsafe")
	}
	return nil
}

func openRuntimeFileIdentity(file *os.File) (runtimeFileIdentity, error) {
	if err := validateOpenRuntimeFile(file); err != nil {
		return runtimeFileIdentity{}, err
	}
	info, err := windowsHandleInfo(syscall.Handle(file.Fd()))
	return runtimeFileIdentity{value: fileIdentity(info)}, err
}

func sameRuntimeFileIdentity(left, right runtimeFileIdentity) bool { return left.value == right.value }

func validateRuntimeFileIdentity(path string, expected runtimeFileIdentity) error {
	handle, info, err := openWindowsObject(path, false)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	if info.NumberOfLinks != 1 || fileIdentity(info) != expected.value {
		return errors.New("controlled file identity changed")
	}
	return nil
}

func validateOpenRuntimeFilePath(file *os.File, path string, expected runtimeFileIdentity) error {
	if file == nil {
		return errors.New("controlled path handle is unavailable")
	}
	handle := syscall.Handle(file.Fd())
	info, err := windowsHandleInfo(handle)
	if err != nil || fileIdentity(info) != expected.value {
		return errors.New("controlled path handle identity changed")
	}
	buffer := make([]uint16, 32768)
	length, _, callErr := getFinalPathNameProc.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if length == 0 || length >= uintptr(len(buffer)) {
		return callErr
	}
	actual := syscall.UTF16ToString(buffer[:length])
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	expectedPath := `\\?\` + absolute
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(expectedPath)) {
		return errors.New("controlled open handle no longer names expected path")
	}
	return nil
}

func removeRuntimeFile(path string) error {
	return removeRuntimeFileWithHook(path, nil)
}

func removeRuntimeFileWithHook(path string, hook func(string)) error {
	return removeRuntimeFileWithHooks(path, hook, nil)
}

func removeRuntimeFileWithHooks(path string, beforeDisposition, afterDisposition func(string)) error {
	handle, err := createWindowsFile(path, deleteAccess|fileReadAttributes|readControl, openExisting)
	if err != nil {
		return err
	}
	closeWith := func(cause error) error {
		if closeErr := syscall.CloseHandle(handle); closeErr != nil {
			return errors.Join(cause, errors.New("close controlled removal handle"))
		}
		return cause
	}
	info, err := windowsHandleInfo(handle)
	if err != nil || info.FileAttributes&(fileAttributeDirectory|fileAttributeReparse) != 0 || info.NumberOfLinks != 1 {
		return closeWith(errors.New("controlled file removal identity is unsafe"))
	}
	if beforeDisposition != nil {
		beforeDisposition(path)
	}
	probe, probeInfo, probeErr := openWindowsObject(path, false)
	if probeErr != nil {
		return closeWith(probeErr)
	}
	if probeErr = syscall.CloseHandle(probe); probeErr != nil {
		return closeWith(errors.New("close controlled removal probe"))
	}
	if fileIdentity(probeInfo) != fileIdentity(info) || probeInfo.NumberOfLinks != 1 {
		return closeWith(errors.New("controlled file changed before removal"))
	}
	deleteFlag := byte(1)
	result, _, callErr := setFileInfoProc.Call(uintptr(handle), uintptr(fileDispositionClass), uintptr(unsafe.Pointer(&deleteFlag)), 1)
	if result == 0 {
		return closeWith(callErr)
	}
	if afterDisposition != nil {
		afterDisposition(path)
	}
	final, err := windowsHandleInfo(handle)
	if err != nil || final.NumberOfLinks > 1 || fileIdentity(final) != fileIdentity(info) {
		return closeWith(errors.New("controlled cleanup identity changed after disposition"))
	}
	if err = closeWith(nil); err != nil {
		return err
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		return errors.New("controlled removal path remains after handle close")
	}
	return nil
}

func removeOpenRuntimeFile(file *os.File, path string, expected runtimeFileIdentity) error {
	if file == nil {
		return errors.New("controlled cleanup handle is unavailable")
	}
	handle := syscall.Handle(file.Fd())
	info, err := windowsHandleInfo(handle)
	if err != nil || info.FileAttributes&(fileAttributeDirectory|fileAttributeReparse) != 0 || info.NumberOfLinks < 1 || fileIdentity(info) != expected.value {
		return errors.New("controlled cleanup handle identity is unsafe")
	}
	if err = validateOpenRuntimeFilePath(file, path, expected); err != nil {
		return err
	}
	beforeDisposition, err := windowsHandleInfo(handle)
	if err != nil || beforeDisposition.NumberOfLinks < 1 || fileIdentity(beforeDisposition) != expected.value {
		return errors.New("controlled cleanup identity changed before disposition")
	}
	deleteFlag := byte(1)
	result, _, callErr := setFileInfoProc.Call(uintptr(handle), uintptr(fileDispositionClass), uintptr(unsafe.Pointer(&deleteFlag)), 1)
	if result == 0 {
		return callErr
	}
	final, err := windowsHandleInfo(handle)
	if err != nil || final.NumberOfLinks > beforeDisposition.NumberOfLinks || fileIdentity(final) != expected.value {
		return errors.New("controlled cleanup identity changed after disposition")
	}
	return nil
}

func createWindowsFile(path string, access uint32, disposition uint32) (syscall.Handle, error) {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	result, _, callErr := createFileProc.Call(uintptr(unsafe.Pointer(pointer)), uintptr(access), uintptr(fileShareRead|fileShareWrite), 0, uintptr(disposition), uintptr(fileFlagOpenReparse), 0)
	handle := syscall.Handle(result)
	if handle == syscall.InvalidHandle {
		return syscall.InvalidHandle, callErr
	}
	return handle, nil
}

func openWindowsObject(path string, directory bool) (syscall.Handle, byHandleFileInformation, error) {
	var empty byHandleFileInformation
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, empty, err
	}
	flags := uint32(fileFlagOpenReparse)
	if directory {
		flags |= fileFlagBackupSemantics
	}
	access := uint32(fileReadAttributes | readControl)
	if directory {
		access |= fileListDirectory | fileAddFile | synchronizeAccess
	}
	result, _, callErr := createFileProc.Call(uintptr(unsafe.Pointer(pointer)), uintptr(access), uintptr(fileShareRead|fileShareWrite), 0, uintptr(openExisting), uintptr(flags), 0)
	handle := syscall.Handle(result)
	if handle == syscall.InvalidHandle {
		return syscall.InvalidHandle, empty, callErr
	}
	info, err := windowsHandleInfo(handle)
	if err != nil || info.FileAttributes&fileAttributeReparse != 0 || directory != (info.FileAttributes&fileAttributeDirectory != 0) {
		_ = syscall.CloseHandle(handle)
		if err == nil {
			err = errors.New("controlled runtime object type is unsafe")
		}
		return syscall.InvalidHandle, empty, err
	}
	return handle, info, nil
}

func windowsHandleInfo(handle syscall.Handle) (byHandleFileInformation, error) {
	var info byHandleFileInformation
	result, _, callErr := getFileInfoProc.Call(uintptr(handle), uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return info, callErr
	}
	return info, nil
}

func fileIdentity(info byHandleFileInformation) windowsFileIdentity {
	return windowsFileIdentity{volume: info.VolumeSerialNumber, indexHigh: info.FileIndexHigh, indexLow: info.FileIndexLow}
}

func windowsOwnerSID(path string) (string, error) {
	const script = `$acl=Get-Acl -LiteralPath $env:G2A06_ACL_PATH;$acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Value`
	command, err := securityPowerShell(script)
	if err != nil {
		return "", err
	}
	command.Env = append(os.Environ(), "G2A06_ACL_PATH="+path)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if !strings.HasPrefix(value, "S-1-") {
		return "", errors.New("invalid owner SID")
	}
	return value, nil
}

func currentUserSID() (string, error) {
	output, err := windowsCommand("whoami.exe", "/user", "/fo", "csv", "/nh").Output()
	if err != nil {
		return "", err
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimSpace(string(output)))).ReadAll()
	if err != nil || len(records) != 1 || len(records[0]) < 2 || !strings.HasPrefix(records[0][1], "S-1-") {
		return "", errors.New("invalid current user SID")
	}
	return records[0][1], nil
}

func verifyWindowsACL(path, sid string) error {
	const script = `$acl=Get-Acl -LiteralPath $env:G2A06_ACL_PATH;if(-not $acl.AreAccessRulesProtected){exit 10};$allowed=@($env:G2A06_ACL_SID,$env:G2A06_SYSTEM_SID,$env:G2A06_ADMIN_SID);$owner=$acl.GetOwner([System.Security.Principal.SecurityIdentifier]).Value;if($owner -ne $env:G2A06_ACL_SID){exit 11};$rules=@($acl.Access);if($rules.Count -ne 3){exit 12};$seen=@{};$full=[int64][System.Security.AccessControl.FileSystemRights]::FullControl;$none=[int][System.Security.AccessControl.PropagationFlags]::None;$expectedInheritance=if([IO.Directory]::Exists($env:G2A06_ACL_PATH)){[int]([System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor [System.Security.AccessControl.InheritanceFlags]::ObjectInherit)}else{[int][System.Security.AccessControl.InheritanceFlags]::None};foreach($r in $rules){$rsid=$r.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value;if([string]$r.AccessControlType -ne $env:G2A06_ALLOW -or $r.IsInherited -or $allowed -notcontains $rsid -or $seen.ContainsKey($rsid)){exit 13};if([int64]$r.FileSystemRights -ne $full){exit 14};if([int]$r.InheritanceFlags -ne $expectedInheritance -or [int]$r.PropagationFlags -ne $none){exit 15};$seen[$rsid]=$true};foreach($required in $allowed){if(-not $seen.ContainsKey($required)){exit 16}}`
	command, err := securityPowerShell(script)
	if err != nil {
		return errors.New("verify controlled runtime ACL")
	}
	command.Env = append(os.Environ(), "G2A06_ACL_PATH="+path, "G2A06_ACL_SID="+sid, "G2A06_SYSTEM_SID=S-1-5-18", "G2A06_ADMIN_SID=S-1-5-32-544", "G2A06_ALLOW=Allow")
	if err = command.Run(); err != nil {
		return errors.New("verify controlled runtime ACL")
	}
	return nil
}

func securityPowerShell(body string) (*exec.Cmd, error) {
	powershell, err := trustedWindowsTool(filepath.Join("WindowsPowerShell", "v1.0", "powershell.exe"))
	if err != nil {
		return nil, err
	}
	const prelude = `$psRoot=[IO.Path]::GetFullPath($PSHOME);$module=[IO.Path]::GetFullPath((Join-Path $psRoot 'Modules\Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1'));$prefix=$psRoot.TrimEnd('\')+'\';if(-not $module.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)-or -not [IO.File]::Exists($module)){exit 80};$current=$module;while($true){if(([IO.File]::GetAttributes($current)-band [IO.FileAttributes]::ReparsePoint)-ne 0){exit 81};if([string]::Equals($current,$psRoot,[StringComparison]::OrdinalIgnoreCase)){break};$parent=[IO.Path]::GetDirectoryName($current);if([string]::IsNullOrEmpty($parent)-or [string]::Equals($parent,$current,[StringComparison]::OrdinalIgnoreCase)){exit 82};$current=$parent};Import-Module -Name $module -Force -ErrorAction Stop;`
	return exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", prelude+body), nil
}

func windowsCommand(name string, arguments ...string) *exec.Cmd {
	path, err := trustedWindowsTool(name)
	if err != nil {
		return exec.Command(filepath.Join("__g2a06_invalid_windows_tool__", name), arguments...)
	}
	return exec.Command(path, arguments...)
}

func trustedWindowsTool(relative string) (string, error) {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("Windows system root unavailable")
	}
	path := filepath.Join(root, "System32", relative)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Windows system tool unavailable or linked")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || !strings.EqualFold(filepath.Clean(abs), filepath.Clean(resolved)) {
		return "", errors.New("Windows system tool contains reparse escape")
	}
	return abs, nil
}

type windowsRenameInfoBuffer struct {
	Raw            []byte
	LengthOffset   int
	FileNameOffset int
	FileNameLength uint32
}

func buildWindowsRenameInfo(destination string) (windowsRenameInfoBuffer, error) {
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil || filepath.VolumeName(absoluteDestination) == "" || strings.HasPrefix(absoluteDestination, `\\`) {
		return windowsRenameInfoBuffer{}, errors.New("controlled replacement destination is not a local absolute path")
	}
	name, err := syscall.UTF16FromString(absoluteDestination)
	if err != nil {
		return windowsRenameInfoBuffer{}, err
	}
	pointerSize := int(unsafe.Sizeof(uintptr(0)))
	rootOffset := (1 + pointerSize - 1) &^ (pointerSize - 1)
	lengthOffset := rootOffset + pointerSize
	nameOffset := lengthOffset + 4
	nameBytes := uint32((len(name) - 1) * 2)
	// FILE_RENAME_INFO excludes the terminator from FileNameLength, but the
	// Win32 FileName member itself is a NUL-terminated DOS path.
	buffer := make([]byte, nameOffset+int(nameBytes)+2)
	buffer[0] = 1
	*(*uint32)(unsafe.Pointer(&buffer[lengthOffset])) = nameBytes
	for index := 0; index < len(name)-1; index++ {
		*(*uint16)(unsafe.Pointer(&buffer[nameOffset+index*2])) = name[index]
	}
	return windowsRenameInfoBuffer{Raw: buffer, LengthOffset: lengthOffset, FileNameOffset: nameOffset, FileNameLength: nameBytes}, nil
}

func replaceOpenRuntimeFile(file *os.File, source, destination string, expected runtimeFileIdentity, hook func(string), postcheck func() error, cleanupOnFailure bool) error {
	if file == nil {
		return errors.New("controlled replacement handle is unavailable")
	}
	handle := syscall.Handle(file.Fd())
	before, err := windowsHandleInfo(handle)
	if err != nil || before.NumberOfLinks != 1 || before.FileAttributes&(fileAttributeDirectory|fileAttributeReparse) != 0 || fileIdentity(before) != expected.value {
		return errors.New("controlled replacement source identity is unsafe")
	}
	sourceDirectory, err := filepath.Abs(filepath.Dir(source))
	if err != nil {
		return err
	}
	destinationDirectory, err := filepath.Abs(filepath.Dir(destination))
	if err != nil || !strings.EqualFold(filepath.Clean(sourceDirectory), filepath.Clean(destinationDirectory)) {
		return errors.New("controlled replacement must remain in one directory")
	}
	renameInfo, err := buildWindowsRenameInfo(destination)
	if err != nil {
		return err
	}
	result, _, callErr := setFileInfoProc.Call(uintptr(handle), uintptr(fileRenameInfoClass), uintptr(unsafe.Pointer(&renameInfo.Raw[0])), uintptr(len(renameInfo.Raw)))
	if result == 0 {
		return callErr
	}
	if hook != nil {
		hook(destination)
	}
	failAfterRename := func(cause error) error {
		if !cleanupOnFailure {
			return cause
		}
		if cleanupErr := disposeOpenRuntimeFile(file, expected); cleanupErr != nil {
			return errors.Join(cause, errors.New("dispose failed controlled replacement"))
		}
		return cause
	}
	if postcheck != nil {
		if postcheckErr := postcheck(); postcheckErr != nil {
			return failAfterRename(postcheckErr)
		}
	}
	after, err := windowsHandleInfo(handle)
	if err != nil || fileIdentity(after) != fileIdentity(before) || after.NumberOfLinks != 1 {
		return failAfterRename(errors.New("controlled replacement identity changed"))
	}
	probe, info, err := openWindowsObject(destination, false)
	if err != nil {
		return failAfterRename(err)
	}
	if closeErr := syscall.CloseHandle(probe); closeErr != nil {
		return failAfterRename(errors.New("close controlled replacement probe"))
	}
	if fileIdentity(info) != fileIdentity(before) || info.NumberOfLinks != 1 {
		return failAfterRename(errors.New("controlled replacement destination identity mismatch"))
	}
	return nil
}

func disposeOpenRuntimeFile(file *os.File, expected runtimeFileIdentity) error {
	if file == nil {
		return errors.New("controlled disposition handle is unavailable")
	}
	handle := syscall.Handle(file.Fd())
	before, err := windowsHandleInfo(handle)
	if err != nil || before.NumberOfLinks != 1 || fileIdentity(before) != expected.value {
		return errors.New("controlled disposition identity is unsafe")
	}
	deleteFlag := byte(1)
	result, _, callErr := setFileInfoProc.Call(uintptr(handle), uintptr(fileDispositionClass), uintptr(unsafe.Pointer(&deleteFlag)), 1)
	if result == 0 {
		return callErr
	}
	after, err := windowsHandleInfo(handle)
	if err != nil || after.NumberOfLinks > 1 || fileIdentity(after) != expected.value {
		return errors.New("controlled disposition identity changed")
	}
	return nil
}

func syncDirectory(path string) error {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	result, _, callErr := createFileProc.Call(uintptr(unsafe.Pointer(pointer)), uintptr(genericWrite|fileReadAttributes|readControl), uintptr(fileShareRead|fileShareWrite), 0, uintptr(openExisting), uintptr(fileFlagOpenReparse|fileFlagBackupSemantics), 0)
	handle := syscall.Handle(result)
	if handle == syscall.InvalidHandle {
		return callErr
	}
	defer syscall.CloseHandle(handle)
	info, err := windowsHandleInfo(handle)
	if err != nil || info.FileAttributes&fileAttributeDirectory == 0 || info.FileAttributes&fileAttributeReparse != 0 {
		return errors.New("controlled directory sync handle is unsafe")
	}
	result, _, callErr = flushFileBuffersProc.Call(uintptr(handle))
	if result == 0 {
		return callErr
	}
	return nil
}
