//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsJunctionEscapeIsRejected(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, ".runtime")
	if err := os.Mkdir(runtimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	junction := filepath.Join(runtimeRoot, "junction")
	if output, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create real Windows junction: %v: %s", err, output)
	}
	if err := ensureControlledDirectory(root, filepath.Join(junction, "child")); err == nil {
		t.Fatal("accepted Windows junction escape")
	}
}

func TestWindowsSharedRuntimeACLIsUntouchedWhileOwnedChildIsProtected(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, ".runtime")
	if err := os.Mkdir(runtimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := windowsCommand("icacls.exe", runtimeRoot, "/inheritance:e", "/grant", "*S-1-5-11:R").CombinedOutput(); err != nil {
		t.Fatalf("broaden shared runtime ACL: %v: %s", err, output)
	}
	before := windowsSDDL(t, runtimeRoot)
	child := filepath.Join(runtimeRoot, "G2A-06", "target")
	if err := ensureControlledDirectory(root, child); err != nil {
		t.Fatal(err)
	}
	if after := windowsSDDL(t, runtimeRoot); after != before {
		t.Fatalf("shared runtime ACL changed\nbefore=%s\nafter=%s", before, after)
	}
	sid, err := currentUserSID()
	if err != nil || verifyWindowsACL(child, sid) != nil {
		t.Fatalf("owned child ACL is not strict: sid_err=%v", err)
	}
}

func TestWindowsG2A06ReparseAndForeignOwnerAreRejected(t *testing.T) {
	t.Run("reparse", func(t *testing.T) {
		root := t.TempDir()
		runtimeRoot := filepath.Join(root, ".runtime")
		if err := os.Mkdir(runtimeRoot, 0700); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		g2a06 := filepath.Join(runtimeRoot, "G2A-06")
		if output, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/J", g2a06, outside).CombinedOutput(); err != nil {
			t.Fatalf("create G2A-06 junction: %v: %s", err, output)
		}
		if err := ensureControlledDirectory(root, filepath.Join(g2a06, "target")); err == nil {
			t.Fatal("accepted reparse G2A-06 directory")
		}
	})

	t.Run("foreign owner", func(t *testing.T) {
		root := t.TempDir()
		g2a06 := filepath.Join(root, ".runtime", "G2A-06")
		if err := os.MkdirAll(g2a06, 0700); err != nil {
			t.Fatal(err)
		}
		original := resolveWindowsOwnerSID
		resolveWindowsOwnerSID = func(path string) (string, error) {
			if strings.EqualFold(filepath.Clean(path), filepath.Clean(g2a06)) {
				return "S-1-5-32-545", nil
			}
			return original(path)
		}
		defer func() { resolveWindowsOwnerSID = original }()
		if err := ensureControlledDirectory(root, filepath.Join(g2a06, "target")); err == nil {
			t.Fatal("accepted foreign-owned G2A-06 directory")
		}
	})
}

func TestWindowsConcurrentParentReplacementFailsClosed(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, ".runtime")
	if err := os.Mkdir(runtimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	g2a06 := filepath.Join(runtimeRoot, "G2A-06")
	outside := t.TempDir()
	replaced := make(chan error, 1)
	stepHook := func(path string) {
		if !strings.EqualFold(filepath.Clean(path), filepath.Clean(g2a06)) {
			return
		}
		go func() {
			backup := g2a06 + ".owned"
			if err := os.Rename(g2a06, backup); err != nil {
				replaced <- err
				return
			}
			output, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/J", g2a06, outside).CombinedOutput()
			if err != nil {
				replaced <- &commandOutputError{err: err, output: string(output)}
				return
			}
			replaced <- nil
		}()
		if err := <-replaced; err != nil {
			t.Fatal(err)
		}
	}
	if err := ensureControlledDirectoryWithHook(root, filepath.Join(g2a06, "target"), stepHook); err == nil {
		t.Fatal("accepted concurrently replaced G2A-06 parent")
	}
}

func TestWindowsHardlinkIsRejectedBeforeACLMutation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ".runtime", "G2A-06")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external-secret.txt")
	if err := os.WriteFile(external, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(directory, "linked-password.txt")
	if output, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/H", linked, external).CombinedOutput(); err != nil {
		t.Fatalf("create hardlink fixture: %v: %s", err, output)
	}
	before := windowsSDDL(t, external)
	if _, err := controlledFile(root, directory, linked); err == nil {
		t.Fatal("accepted controlled hardlink")
	}
	if after := windowsSDDL(t, external); after != before {
		t.Fatalf("external inode ACL changed\nbefore=%s\nafter=%s", before, after)
	}
}

func TestWindowsControlledIORaceWindows(t *testing.T) {
	t.Run("validate before create", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		hooks := &controlledRaceHooks{afterValidateBeforeCreate: func(path string) {
			if output, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/J", path, outside).CombinedOutput(); err != nil {
				t.Fatalf("replace create target: %v: %s", err, output)
			}
		}}
		if err := ensureControlledDirectoryWithHooks(root, filepath.Join(root, ".runtime", "G2A-06"), hooks); err == nil {
			t.Fatal("accepted create target replacement")
		}
	})

	t.Run("secure before write", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		leak := filepath.Join(directory, "leak-before-write")
		attackFailed := false
		hooks := &controlledRaceHooks{afterSecureBeforeWrite: func(path string) {
			if _, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/H", leak, path).CombinedOutput(); err != nil {
				attackFailed = true
			}
		}}
		err := atomicWriteWithTestHooks(root, filepath.Join(directory, "secret.json"), []byte("secret-marker"), hooks)
		if !attackFailed && err == nil {
			t.Fatal("hardlink race was neither blocked nor detected")
		}
		if raw, readErr := os.ReadFile(leak); readErr == nil && bytes.Contains(raw, []byte("secret-marker")) {
			t.Fatal("secret escaped through pre-write hardlink")
		}
	})

	t.Run("close before move", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		leak := filepath.Join(directory, "leak-before-move")
		attackFailed := false
		hooks := &controlledRaceHooks{afterCloseBeforeMove: func(path string) {
			if _, err := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/H", leak, path).CombinedOutput(); err != nil {
				attackFailed = true
			}
		}}
		if err := atomicWriteWithTestHooks(root, filepath.Join(directory, "secret.json"), []byte("secret-marker"), hooks); err == nil || attackFailed {
			t.Fatalf("post-write hardlink was not detected: err=%v attack_blocked=%v", err, attackFailed)
		}
		if _, err := os.Stat(leak); err != nil {
			t.Fatal("hardlink race fixture was not created inside the protected directory")
		}
	})

	t.Run("move before postcheck", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		attackFailed := false
		hooks := &controlledRaceHooks{afterMoveBeforePostcheck: func(path string) {
			if err := os.Rename(path, path+".replaced"); err != nil {
				attackFailed = true
			}
		}}
		target := filepath.Join(directory, "secret.json")
		if err := atomicWriteWithTestHooks(root, target, []byte("secret-marker"), hooks); err != nil {
			t.Fatal(err)
		}
		if !attackFailed {
			t.Fatal("destination rename was not blocked by live handle")
		}
	})

	t.Run("verify before read", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		path := filepath.Join(directory, "secret.json")
		if err := atomicWriteControlled(root, path, []byte("secret-marker")); err != nil {
			t.Fatal(err)
		}
		attackFailed := false
		raw, err := readControlledFileWithHooks(path, &controlledRaceHooks{afterVerifyBeforeRead: func(path string) {
			if renameErr := os.Rename(path, path+".replaced"); renameErr != nil {
				attackFailed = true
			}
		}})
		if err != nil || string(raw) != "secret-marker" || !attackFailed {
			t.Fatalf("read race err=%v attack_blocked=%v", err, attackFailed)
		}
	})

	t.Run("hardlink after read", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		path := filepath.Join(directory, "secret.json")
		if err := atomicWriteControlled(root, path, []byte("secret-marker")); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "read-race-link.json")
		attackFailed := false
		raw, err := readControlledFileWithHooks(path, &controlledRaceHooks{afterReadBeforePostcheck: func(path string) {
			if output, linkErr := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/H", link, path).CombinedOutput(); linkErr != nil {
				attackFailed = true
				t.Logf("hardlink attack was blocked: %v: %s", linkErr, output)
			}
		}})
		if attackFailed {
			t.Skip("filesystem blocked the hardlink race")
		}
		if err == nil || len(raw) != 0 {
			t.Fatalf("read returned bytes after hardlink race: len=%d err=%v", len(raw), err)
		}
	})

	t.Run("reservation failures are identity-cleaned", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			hooks *controlledRaceHooks
		}{
			{name: "secure", hooks: &controlledRaceHooks{reservationSecure: func(string, bool) error { return os.ErrPermission }}},
			{name: "write", hooks: &controlledRaceHooks{reservationWrite: func(file *os.File, _ string) error {
				_, _ = file.WriteString("partial")
				return os.ErrPermission
			}}},
			{name: "sync", hooks: &controlledRaceHooks{reservationSync: func(*os.File) error { return os.ErrPermission }}},
		} {
			t.Run(test.name, func(t *testing.T) {
				root := t.TempDir()
				if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := reserveAcceptanceWithHooks(root, test.hooks); err == nil {
					t.Fatal("injected reservation failure was ignored")
				}
				if _, err := os.Lstat(reservationFile(root)); !os.IsNotExist(err) {
					t.Fatalf("failed reservation was not identity-cleaned: %v", err)
				}
			})
		}
	})

	t.Run("reservation post-write hardlink removes only original entry", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "reservation-race-link.json")
		attackFailed := false
		err := reserveAcceptanceWithHooks(root, &controlledRaceHooks{afterReservationWrite: func(path string) {
			if _, linkErr := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/H", link, path).CombinedOutput(); linkErr != nil {
				attackFailed = true
			}
		}})
		if attackFailed {
			t.Skip("filesystem blocked the hardlink race")
		}
		if err == nil {
			t.Fatalf("post-write hardlink did not fail safely: %v", err)
		}
		if _, statErr := os.Lstat(reservationFile(root)); !os.IsNotExist(statErr) {
			t.Fatalf("original reservation entry remained: %v", statErr)
		}
		raw, readErr := os.ReadFile(link)
		if readErr != nil || string(raw) != reservationRecord {
			t.Fatalf("attacker link was changed or removed: raw=%q err=%v", raw, readErr)
		}
		if err := reserveAcceptance(root); err != nil {
			t.Fatalf("second reserve failed after original-entry cleanup: %v", err)
		}
		if err := removeAcceptanceState(root); err != nil {
			t.Fatal(err)
		}
		if raw, readErr = os.ReadFile(link); readErr != nil || string(raw) != reservationRecord {
			t.Fatalf("normal cleanup deleted unknown attacker link: raw=%q err=%v", raw, readErr)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("verify before remove", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		path := filepath.Join(directory, "secret.json")
		if err := atomicWriteControlled(root, path, []byte("secret-marker")); err != nil {
			t.Fatal(err)
		}
		attackFailed := false
		err := removeRuntimeFileWithHook(path, func(path string) {
			if renameErr := os.Rename(path, path+".replaced"); renameErr != nil {
				attackFailed = true
			}
		})
		if err != nil || !attackFailed {
			t.Fatalf("remove race err=%v attack_blocked=%v", err, attackFailed)
		}
	})

	t.Run("hardlink after remove disposition", func(t *testing.T) {
		root, directory := controlledTestDirectory(t)
		path := filepath.Join(directory, "secret.json")
		if err := atomicWriteControlled(root, path, []byte("secret-marker")); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "remove-race-link.json")
		attackFailed := false
		err := removeRuntimeFileWithHooks(path, nil, func(path string) {
			if output, linkErr := windowsCommand("cmd.exe", "/d", "/c", "mklink", "/H", link, path).CombinedOutput(); linkErr != nil {
				attackFailed = true
				t.Logf("post-disposition hardlink attack was blocked: %v: %s", linkErr, output)
			}
		})
		if err == nil && !attackFailed {
			t.Fatal("post-disposition hardlink race was neither blocked nor detected")
		}
		if err == nil {
			if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
				t.Fatalf("disposed path still exists: %v", statErr)
			}
		}
	})
}

func controlledTestDirectory(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, ".runtime", "G2A-06")
	if err := ensureControlledDirectory(root, directory); err != nil {
		t.Fatal(err)
	}
	return root, directory
}

func atomicWriteWithTestHooks(root, path string, raw []byte, hooks *controlledRaceHooks) error {
	directory := filepath.Dir(path)
	guard, err := lockRuntimeDirectory(directory)
	if err != nil {
		return err
	}
	defer guard.Close()
	return atomicWriteControlledGuard(root, path, raw, guard, hooks)
}

type commandOutputError struct {
	err    error
	output string
}

func (e *commandOutputError) Error() string { return e.err.Error() + ": " + e.output }

func windowsSDDL(t *testing.T, path string) string {
	t.Helper()
	command, err := securityPowerShell(`(Get-Acl -LiteralPath $env:G2A06_ACL_PATH).Sddl`)
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "G2A06_ACL_PATH="+path)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func TestWindowsBroadACLIsDetectedThenRemediated(t *testing.T) {
	poisoned := filepath.Join(t.TempDir(), "Modules")
	fakeModule := filepath.Join(poisoned, "Microsoft.PowerShell.Security")
	if err := os.MkdirAll(fakeModule, 0700); err != nil || os.WriteFile(filepath.Join(fakeModule, "Microsoft.PowerShell.Security.psd1"), []byte(`throw 'poisoned module loaded'`), 0600) != nil {
		t.Fatal("create poisoned PSModulePath")
	}
	t.Setenv("PSModulePath", poisoned)
	path := filepath.Join(t.TempDir(), "acceptance-state.json")
	if err := os.WriteFile(path, []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if output, commandErr := windowsCommand("icacls.exe", path, "/inheritance:e", "/grant", "*S-1-5-11:R").CombinedOutput(); commandErr != nil {
		t.Fatalf("create broad ACL fixture: %v: %s", commandErr, output)
	}
	if err = verifyWindowsACL(path, sid); err == nil {
		t.Fatal("broad inherited ACL was not rejected")
	}
	if err = secureRuntimePath(path, false); err != nil {
		t.Fatal(err)
	}
	if err = verifyWindowsACL(path, sid); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsACLRejectsMissingDowngradedAndDenyRules(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing system", args: []string{"/remove:g", "*S-1-5-18"}},
		{name: "downgraded administrators", args: []string{"/grant:r", "*S-1-5-32-544:R"}},
		{name: "extra allow", args: []string{"/grant", "*S-1-5-11:R"}},
		{name: "extra deny", args: []string{"/deny", "*S-1-5-11:R"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "acceptance-state.json")
			if err := os.WriteFile(path, []byte("state"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := secureRuntimePath(path, false); err != nil {
				t.Fatal(err)
			}
			arguments := append([]string{path}, test.args...)
			if output, commandErr := windowsCommand("icacls.exe", arguments...).CombinedOutput(); commandErr != nil {
				t.Fatalf("mutate ACL fixture: %v: %s", commandErr, output)
			}
			if err := verifyWindowsACL(path, sid); err == nil {
				t.Fatal("unsafe ACL mutation was accepted")
			}
			if test.name == "extra deny" {
				if output, commandErr := windowsCommand("icacls.exe", path, "/remove:d", "*S-1-5-11").CombinedOutput(); commandErr != nil {
					t.Fatalf("remove deny fixture: %v: %s", commandErr, output)
				}
			}
		})
	}
}

func TestWindowsACLRejectsWrongInheritanceAndPropagation(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		inheritance string
		propagation string
	}{
		{name: "directory no inheritance", inheritance: "None", propagation: "None"},
		{name: "directory no propagate", inheritance: "ContainerInherit,ObjectInherit", propagation: "NoPropagateInherit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "controlled")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			const script = `$acl=New-Object System.Security.AccessControl.DirectorySecurity;$owner=New-Object System.Security.Principal.SecurityIdentifier($env:G2A06_ACL_SID);$acl.SetOwner($owner);$acl.SetAccessRuleProtection($true,$false);$full=[System.Security.AccessControl.FileSystemRights]::FullControl;$allow=[System.Security.AccessControl.AccessControlType]::Allow;foreach($value in @($env:G2A06_ACL_SID,$env:G2A06_SYSTEM_SID,$env:G2A06_ADMIN_SID)){$principal=New-Object System.Security.Principal.SecurityIdentifier($value);if($value -eq $env:G2A06_ACL_SID){$inherit=[System.Security.AccessControl.InheritanceFlags]$env:G2A06_TEST_INHERIT;$prop=[System.Security.AccessControl.PropagationFlags]$env:G2A06_TEST_PROP}else{$inherit=[System.Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit';$prop=[System.Security.AccessControl.PropagationFlags]::None};$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule($principal,$full,$inherit,$prop,$allow)))|Out-Null};Set-Acl -LiteralPath $env:G2A06_ACL_PATH -AclObject $acl`
			command, commandErr := securityPowerShell(script)
			if commandErr != nil {
				t.Fatal(commandErr)
			}
			command.Env = append(os.Environ(), "G2A06_ACL_PATH="+path, "G2A06_ACL_SID="+sid, "G2A06_SYSTEM_SID=S-1-5-18", "G2A06_ADMIN_SID=S-1-5-32-544", "G2A06_TEST_INHERIT="+test.inheritance, "G2A06_TEST_PROP="+test.propagation)
			if output, commandErr := command.CombinedOutput(); commandErr != nil {
				t.Fatalf("mutate inheritance fixture: %v: %s", commandErr, output)
			}
			if err := verifyWindowsACL(path, sid); err == nil {
				t.Fatal("unsafe ACL inheritance or propagation was accepted")
			}
		})
	}
}

func TestWindowsRepositoryVolumeUnicodeReservationRename(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot, err := locateRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(repositoryRoot, ".runtime", "G2A-06")
	root, err := os.MkdirTemp(parent, "路径原子改名回归-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(root); removeErr != nil {
			t.Errorf("remove repository-volume regression root: %v", removeErr)
		}
	})
	if err := os.Mkdir(filepath.Join(root, ".runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := reserveAcceptance(root); err != nil {
		t.Fatal(err)
	}
	if err := markAcceptancePreparing(root); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(reservationFile(root))
	if err != nil || string(raw) != reservationPreparingRecord {
		t.Fatalf("repository-volume prepare marker raw=%q err=%v", raw, err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(reservationFile(root)), "acceptance-reservation.json*"))
	if err != nil || len(matches) != 1 || !strings.EqualFold(filepath.Clean(matches[0]), filepath.Clean(reservationFile(root))) {
		t.Fatalf("rename created malformed directory entries: matches=%v err=%v", matches, err)
	}
	if recovered, recoverErr := recoverReservationOnly(root); recovered || recoverErr == nil || !strings.Contains(recoverErr.Error(), "manual audit required") {
		t.Fatalf("preparing marker recovery=(%v,%v)", recovered, recoverErr)
	}
	if err := removeAcceptanceState(root); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRenameInfoHasExactLengthAndTerminatingNUL(t *testing.T) {
	destination := filepath.Join(`D:\受控仓库`, "目录", "acceptance-reservation.json")
	info, err := buildWindowsRenameInfo(destination)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		t.Fatal(err)
	}
	units, err := syscall.UTF16FromString(absolute)
	if err != nil {
		t.Fatal(err)
	}
	expectedLength := uint32((len(units) - 1) * 2)
	storedLength := binary.LittleEndian.Uint32(info.Raw[info.LengthOffset : info.LengthOffset+4])
	if info.FileNameLength != expectedLength || storedLength != expectedLength {
		t.Fatalf("FileNameLength=%d stored=%d expected=%d", info.FileNameLength, storedLength, expectedLength)
	}
	if len(info.Raw) != info.FileNameOffset+int(expectedLength)+2 {
		t.Fatalf("rename buffer length=%d expected=%d", len(info.Raw), info.FileNameOffset+int(expectedLength)+2)
	}
	decoded := make([]uint16, len(units))
	for index := range decoded {
		decoded[index] = binary.LittleEndian.Uint16(info.Raw[info.FileNameOffset+index*2 : info.FileNameOffset+index*2+2])
	}
	if decoded[len(decoded)-1] != 0 || syscall.UTF16ToString(decoded) != absolute {
		t.Fatalf("rename FileName is not exact NUL-terminated DOS path: %q", syscall.UTF16ToString(decoded))
	}
}
