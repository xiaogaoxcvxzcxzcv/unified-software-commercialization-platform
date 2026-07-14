package machinecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/gowebpki/jcs"
)

func Canonicalize(document []byte) ([]byte, error) {
	canonical, err := jcs.Transform(document)
	if err != nil {
		return nil, fmt.Errorf("RFC 8785 transform: %w", err)
	}
	return canonical, nil
}

func Digest(document []byte) (string, error) {
	canonical, err := Canonicalize(document)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// DigestWithoutTopLevelField avoids self-referential checksums while retaining
// RFC 8785 normalization for every other field in the document.
func DigestWithoutTopLevelField(document []byte, field string) (string, error) {
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		return "", fmt.Errorf("parse JSON for digest: %w", err)
	}
	if _, exists := value[field]; !exists {
		return "", fmt.Errorf("digest field %q is missing", field)
	}
	delete(value, field)
	withoutField, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal JSON for digest: %w", err)
	}
	digest, err := Digest(withoutField)
	if err != nil {
		return "", err
	}
	return "sha256:" + digest, nil
}
