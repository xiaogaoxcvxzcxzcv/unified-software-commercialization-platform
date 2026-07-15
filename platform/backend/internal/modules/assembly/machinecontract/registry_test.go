package machinecontract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestRegistryCompilesEverySchemaAndValidatesFixtures(t *testing.T) {
	root := repositoryRoot(t)
	registry, err := LoadDirectory(filepath.Join(root, "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"assembly-manifest", "assembly-plan", "assembly-run", "catalog-snapshot", "common",
		"extension-manifest", "feature-block-catalog", "generated-project-lock", "generator-commit-journal", "generator-diagnostic", "generator-eject-plan",
		"generator-request", "generator-result", "generator-rollback-point", "package-manifest",
		"product-blueprint", "tool-manifest", "ui-template-manifest",
	}
	if names := registry.Names(); !equalStrings(names, wantNames) {
		t.Fatalf("schema names = %v, want %v", names, wantNames)
	}

	fixtureRoot := filepath.Join(root, "platform", "contracts", "schemas", "fixtures")
	validated := 0
	err = filepath.WalkDir(fixtureRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil
		}
		validated++
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		schemaName := strings.SplitN(entry.Name(), ".", 2)[0]
		wantValid := strings.HasSuffix(entry.Name(), ".valid.json")
		if len(parts) >= 3 && parts[0] == "assembly-generator" {
			schemaName = parts[1]
			wantValid = strings.HasPrefix(entry.Name(), "valid")
		}
		validationErr := registry.Validate(schemaName, contents)
		if wantValid && validationErr != nil {
			t.Errorf("valid fixture %s rejected: %v", filepath.ToSlash(path), validationErr)
		}
		if !wantValid && validationErr == nil {
			t.Errorf("invalid fixture %s accepted", filepath.ToSlash(path))
		}
		if wantValid {
			assertStableDigestAfterRemarshal(t, contents)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if validated < 70 {
		t.Fatalf("only %d machine contract fixtures were validated", validated)
	}
}

func TestRegistryRejectsUnknownSchema(t *testing.T) {
	registry, err := LoadDirectory(filepath.Join(repositoryRoot(t), "platform", "contracts", "schemas", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate("missing", []byte(`{}`)); !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("unknown schema error = %v", err)
	}
}

func assertStableDigestAfterRemarshal(t *testing.T, contents []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		t.Fatal(err)
	}
	remarshaled, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Digest(contents)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Digest(remarshaled)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical digest changed after equivalent JSON remarshal: %s != %s", first, second)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve machine contract test path")
	}
	directory := filepath.Dir(filename)
	for {
		candidate := filepath.Join(directory, "platform", "contracts", "schemas", "v1")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func equalStrings(first, second []string) bool {
	first = append([]string(nil), first...)
	second = append([]string(nil), second...)
	sort.Strings(first)
	sort.Strings(second)
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}
