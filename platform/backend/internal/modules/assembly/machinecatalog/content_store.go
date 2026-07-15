package machinecatalog

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

// ReadLockedSource revalidates the immutable catalog version before returning
// bytes. Absolute catalog paths never cross the generator port.
func (c *Catalog) ReadLockedSource(sourceID, version, manifestChecksum, sourcePath, sourceChecksum string) ([]byte, error) {
	if c == nil || c.contracts == nil || sourceID == "" || version == "" || manifestChecksum == "" || sourceChecksum == "" {
		return nil, ErrUnknownContent
	}
	if err := machinecontract.ValidateSafeRelativePath(sourcePath); err != nil {
		return nil, err
	}
	key := sourceID + "\x00" + version
	document, packageSource := c.packageSources[key]
	if !packageSource {
		document = c.templateSources[key]
	}
	if document.versionRoot == "" {
		return nil, ErrUnknownContent
	}
	var expectedManifest, expectedTree string
	var files []ContentFile
	if packageSource {
		var manifest PackageManifest
		if err := json.Unmarshal(document.contents, &manifest); err != nil {
			return nil, err
		}
		expectedManifest, expectedTree, files = manifest.ManifestSHA256, manifest.ContentTreeSHA256, manifest.ContentFiles
	} else {
		var manifest TemplateManifest
		if err := json.Unmarshal(document.contents, &manifest); err != nil {
			return nil, err
		}
		expectedManifest, expectedTree, files = manifest.ManifestSHA256, manifest.ContentTreeSHA256, manifest.ContentFiles
	}
	if subtle.ConstantTimeCompare([]byte(expectedManifest), []byte(manifestChecksum)) != 1 {
		return nil, ErrChecksumMismatch
	}
	if err := validateDocumentIntegrity(document, expectedManifest, files, expectedTree); err != nil {
		return nil, err
	}
	declared := false
	for _, file := range files {
		if file.Path == sourcePath && subtle.ConstantTimeCompare([]byte(file.SHA256), []byte(sourceChecksum)) == 1 {
			declared = true
			break
		}
	}
	if !declared {
		return nil, ErrUnknownContent
	}
	absolute := filepath.Join(document.versionRoot, filepath.FromSlash(sourcePath))
	if err := validateRegularFileInside(document.versionRoot, absolute); err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(absolute)
	if err != nil {
		return nil, fmt.Errorf("read locked source: %w", err)
	}
	digest := sha256.Sum256(contents)
	actual := "sha256:" + hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(strings.ToLower(sourceChecksum))) != 1 {
		return nil, ErrChecksumMismatch
	}
	return append([]byte(nil), contents...), nil
}
