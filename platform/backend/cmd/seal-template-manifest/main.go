package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type contentFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

func main() {
	catalogRoot := flag.String("catalog-root", "", "authorized template catalog root")
	manifestPath := flag.String("template", "", "template.json to seal")
	schemaDirectory := flag.String("schema-directory", "../contracts/schemas/v1", "machine contract schema directory")
	flag.Parse()
	if err := run(*catalogRoot, *manifestPath, *schemaDirectory); err != nil {
		fmt.Fprintln(os.Stderr, "seal template manifest:", err)
		os.Exit(1)
	}
}

func run(catalogRoot, manifestPath, schemaDirectory string) error {
	root, err := filepath.Abs(catalogRoot)
	if err != nil || strings.TrimSpace(catalogRoot) == "" {
		return errors.New("catalog root is required")
	}
	manifest, err := filepath.Abs(manifestPath)
	if err != nil || filepath.Base(manifest) != "template.json" {
		return errors.New("template must name a template.json")
	}
	if !within(manifest, root) {
		return errors.New("template leaves authorized catalog root")
	}
	versionRoot := filepath.Dir(manifest)
	if resolved, resolveErr := filepath.EvalSymlinks(versionRoot); resolveErr != nil || !samePath(resolved, versionRoot) {
		return errors.New("template version root must not use links")
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return err
	}
	files, digests, err := scanFiles(versionRoot, manifest)
	if err != nil {
		return err
	}
	entrypoints, ok := document["entrypoints"].([]any)
	if !ok || len(entrypoints) == 0 {
		return errors.New("entrypoints must be a non-empty array")
	}
	for _, value := range entrypoints {
		entrypoint, ok := value.(map[string]any)
		if !ok {
			return errors.New("entrypoint must be an object")
		}
		source, ok := entrypoint["source_path"].(string)
		if !ok || digests[source] == "" {
			return fmt.Errorf("entrypoint source is not a regular content file: %v", entrypoint["source_path"])
		}
		entrypoint["source_sha256"] = digests[source]
	}
	document["content_files"] = files
	treeRaw, err := json.Marshal(files)
	if err != nil {
		return err
	}
	treeDigest, err := machinecontract.Digest(treeRaw)
	if err != nil {
		return err
	}
	document["content_tree_sha256"] = "sha256:" + treeDigest
	document["manifest_sha256"] = "sha256:" + strings.Repeat("0", 64)
	unsigned, err := json.Marshal(document)
	if err != nil {
		return err
	}
	manifestDigest, err := machinecontract.DigestWithoutTopLevelField(unsigned, "manifest_sha256")
	if err != nil {
		return err
	}
	document["manifest_sha256"] = manifestDigest
	sealed, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	sealed = append(sealed, '\n')
	registry, err := machinecontract.LoadDirectory(schemaDirectory)
	if err != nil {
		return err
	}
	if err := registry.Validate("ui-template-manifest", sealed); err != nil {
		return err
	}
	return os.WriteFile(manifest, sealed, 0o600)
}

func scanFiles(root, manifest string) ([]contentFile, map[string]string, error) {
	files := make([]contentFile, 0)
	digests := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if samePath(path, manifest) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked catalog content is forbidden: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular catalog content is forbidden: %s", path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !samePath(resolved, path) {
			return fmt.Errorf("linked catalog content is forbidden: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if err := machinecontract.ValidateSafeRelativePath(relative); err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		value := "sha256:" + hex.EncodeToString(digest[:])
		files = append(files, contentFile{Path: relative, SHA256: value, Kind: "file"})
		digests[relative] = value
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, digests, nil
}

func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
