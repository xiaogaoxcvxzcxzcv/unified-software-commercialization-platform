package core

import (
	"encoding/json"
	"fmt"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type RegistryValidator struct{ registry *machinecontract.Registry }

func NewRegistryValidator(registry *machinecontract.Registry) *RegistryValidator {
	return &RegistryValidator{registry: registry}
}

func (v *RegistryValidator) Validate(schemaName string, document json.RawMessage) (ValidatedDocument, error) {
	if v == nil || v.registry == nil || len(document) == 0 {
		return ValidatedDocument{}, ErrDocumentInvalid
	}
	if err := v.registry.Validate(schemaName, document); err != nil {
		return ValidatedDocument{}, fmt.Errorf("%w: %v", ErrDocumentInvalid, err)
	}
	canonical, err := machinecontract.Canonicalize(document)
	if err != nil {
		return ValidatedDocument{}, fmt.Errorf("%w: %v", ErrDocumentInvalid, err)
	}
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(canonical, &header); err != nil || header.SchemaVersion == "" {
		return ValidatedDocument{}, ErrDocumentInvalid
	}
	digest, err := machinecontract.Digest(canonical)
	if err != nil {
		return ValidatedDocument{}, fmt.Errorf("%w: %v", ErrDocumentInvalid, err)
	}
	return ValidatedDocument{SchemaName: schemaName, SchemaVersion: header.SchemaVersion, CanonicalJSON: canonical, SHA256: "sha256:" + digest}, nil
}

func verifiedEmbeddedDigest(document json.RawMessage, field string) (string, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(document, &values); err != nil {
		return "", fmt.Errorf("%w: %v", ErrDocumentInvalid, err)
	}
	var claimed string
	if err := json.Unmarshal(values[field], &claimed); err != nil || claimed == "" {
		return "", fmt.Errorf("%w: missing %s", ErrDocumentInvalid, field)
	}
	actual, err := machinecontract.DigestWithoutTopLevelField(document, field)
	if err != nil || !digestsEqual(claimed, actual) {
		return "", fmt.Errorf("%w: %s mismatch", ErrDocumentInvalid, field)
	}
	return claimed, nil
}
