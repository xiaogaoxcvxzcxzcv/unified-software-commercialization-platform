package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunRendersLockedStandardATemplateWithoutCreatingCustomCode(t *testing.T) {
	root := repositoryRoot(t)
	runtimeRoot := filepath.Join(root, ".runtime")
	if err := os.MkdirAll(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := os.MkdirTemp(runtimeRoot, "template-preview-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(output)

	if err := run(root, "standard-a", "0.1.0", "web", output, "模板验证软件"); err != nil {
		t.Fatal(err)
	}
	mainSource, err := os.ReadFile(filepath.Join(output, "src", "main.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSource), `const productName = "模板验证软件";`) || strings.Contains(string(mainSource), "{{") {
		t.Fatalf("main source was not strictly rendered: %s", mainSource)
	}
	routes, err := os.ReadFile(filepath.Join(output, "src", "integration", "routes.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(routes), "// <platform-generated:standard-a.routes>") || !strings.Contains(string(routes), "// </platform-generated:standard-a.routes>") {
		t.Fatalf("integration source lacks generated-region markers: %s", routes)
	}
	if _, err := os.Stat(filepath.Join(output, "src", "custom")); !os.IsNotExist(err) {
		t.Fatalf("generator must not create custom product code: %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	directory := filepath.Dir(filename)
	for {
		if info, err := os.Stat(filepath.Join(directory, "platform", "contracts", "schemas", "v1")); err == nil && info.IsDir() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}
