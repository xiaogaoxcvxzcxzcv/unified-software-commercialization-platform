package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

var beforeManifestWrite = func() {}
var afterManifestPathCheck = func() {}

func main() {
	catalogRoot := flag.String("catalog-root", "", "authorized package catalog root")
	manifestPath := flag.String("package", "", "manifest.json to seal")
	schemaDirectory := flag.String("schema-directory", "../contracts/schemas/v1", "machine contract schema directory")
	flag.Parse()
	if err := run(*catalogRoot, *manifestPath, *schemaDirectory); err != nil {
		fmt.Fprintln(os.Stderr, "seal package manifest:", err)
		os.Exit(1)
	}
}

func run(catalogRoot, manifestPath, schemaDirectory string) error {
	root, err := filepath.Abs(catalogRoot)
	if err != nil || strings.TrimSpace(catalogRoot) == "" {
		return errors.New("catalog root is required")
	}
	manifest, err := filepath.Abs(manifestPath)
	if err != nil || filepath.Base(manifest) != "manifest.json" {
		return errors.New("package must name a manifest.json")
	}
	if !within(manifest, root) {
		return errors.New("package leaves authorized catalog root")
	}
	versionRoot := filepath.Dir(manifest)
	if resolved, resolveErr := filepath.EvalSymlinks(versionRoot); resolveErr != nil || !samePath(resolved, versionRoot) {
		return errors.New("package version root must not use links")
	}
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
		return errors.New("package manifest must be an unlinked regular file")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(manifest); resolveErr != nil || !samePath(resolved, manifest) {
		return errors.New("package manifest must not use links")
	}
	manifestFile, err := os.OpenFile(manifest, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer manifestFile.Close()
	openedInfo, err := manifestFile.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return errors.New("package manifest changed while it was being opened")
	}
	raw, err := io.ReadAll(manifestFile)
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
	outputs, ok := document["generated_outputs"].([]any)
	if !ok {
		return errors.New("generated_outputs must be an array")
	}
	for _, value := range outputs {
		output, ok := value.(map[string]any)
		if !ok {
			return errors.New("generated output must be an object")
		}
		source, ok := output["source_path"].(string)
		if !ok {
			return errors.New("generated output source_path must be a string")
		}
		if err := machinecontract.ValidateSafeRelativePath(source); err != nil {
			return err
		}
		if digests[source] == "" {
			return fmt.Errorf("generated output source is not a regular content file: %s", source)
		}
		output["source_sha256"] = digests[source]
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
	if err := registry.Validate("package-manifest", sealed); err != nil {
		return err
	}
	beforeManifestWrite()
	currentInfo, err := os.Lstat(manifest)
	if err != nil || !currentInfo.Mode().IsRegular() || currentInfo.Mode()&fs.ModeSymlink != 0 ||
		!os.SameFile(info, currentInfo) {
		return errors.New("package manifest changed while it was being sealed")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(manifest); resolveErr != nil ||
		!samePath(resolved, manifest) {
		return errors.New("package manifest changed to a link while it was being sealed")
	}
	if _, err := manifestFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	currentRaw, err := io.ReadAll(manifestFile)
	if err != nil || !bytes.Equal(currentRaw, raw) {
		return errors.New("package manifest bytes changed while it was being sealed")
	}
	afterManifestPathCheck()
	if _, err := manifestFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := manifestFile.Truncate(0); err != nil {
		return err
	}
	if _, err := io.Copy(manifestFile, bytes.NewReader(sealed)); err != nil {
		return err
	}
	if err := manifestFile.Sync(); err != nil {
		return err
	}
	finalInfo, err := os.Lstat(manifest)
	if err != nil || !finalInfo.Mode().IsRegular() || finalInfo.Mode()&fs.ModeSymlink != 0 ||
		!os.SameFile(openedInfo, finalInfo) {
		return errors.New("package manifest path changed during write")
	}
	return nil
}

func scanFiles(root, manifest string) ([]contentFile, map[string]string, error) {
	files := make([]contentFile, 0)
	digests := make(map[string]string)
	foldedPaths := make(map[string]string)
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
		folded := strings.ToLower(relative)
		if previous, collision := foldedPaths[folded]; collision {
			return fmt.Errorf("catalog content paths collide by case: %s and %s", previous, relative)
		}
		foldedPaths[folded] = relative

		contents, err := readRegularUnlinkedFile(path, info)
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

func readRegularUnlinkedFile(path string, expected fs.FileInfo) ([]byte, error) {
	initial, err := os.Lstat(path)
	if err != nil || !initial.Mode().IsRegular() || initial.Mode()&fs.ModeSymlink != 0 ||
		!os.SameFile(expected, initial) {
		return nil, fmt.Errorf("catalog content changed before open: %s", path)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr != nil || !samePath(resolved, path) {
		return nil, fmt.Errorf("linked catalog content is forbidden: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return nil, fmt.Errorf("catalog content changed while opening: %s", path)
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	final, err := os.Lstat(path)
	if err != nil || !final.Mode().IsRegular() || final.Mode()&fs.ModeSymlink != 0 ||
		!os.SameFile(opened, final) {
		return nil, fmt.Errorf("catalog content changed while reading: %s", path)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr != nil || !samePath(resolved, path) {
		return nil, fmt.Errorf("linked catalog content is forbidden: %s", path)
	}
	return contents, nil
}

func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
