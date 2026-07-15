package machinecatalog

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type BlockDefinition struct {
	BlockID   string `json:"block_id"`
	Surface   string `json:"surface"`
	Readiness string `json:"readiness"`
}

type blockCatalogDocument struct {
	SchemaVersion  string            `json:"schema_version"`
	CatalogVersion string            `json:"catalog_version"`
	Blocks         []BlockDefinition `json:"blocks"`
	CatalogSHA256  string            `json:"catalog_sha256"`
}

type BlockCatalog struct {
	version  string
	checksum string
	byID     map[string]BlockDefinition
}

func LoadBlockCatalog(path string, contracts *machinecontract.Registry) (*BlockCatalog, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read feature block catalog: %w", err)
	}
	if err := contracts.Validate("feature-block-catalog", contents); err != nil {
		return nil, err
	}
	var document blockCatalogDocument
	if err := json.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("parse feature block catalog: %w", err)
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(contents, "catalog_sha256")
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(document.CatalogSHA256)) != 1 {
		return nil, fmt.Errorf("%w: feature block catalog", ErrChecksumMismatch)
	}
	if !sort.SliceIsSorted(document.Blocks, func(i, j int) bool { return document.Blocks[i].BlockID < document.Blocks[j].BlockID }) {
		return nil, fmt.Errorf("feature blocks must be sorted by block_id")
	}
	byID := make(map[string]BlockDefinition, len(document.Blocks))
	for _, block := range document.Blocks {
		if _, duplicate := byID[block.BlockID]; duplicate {
			return nil, fmt.Errorf("duplicate feature block %q", block.BlockID)
		}
		byID[block.BlockID] = block
	}
	return &BlockCatalog{version: document.CatalogVersion, checksum: document.CatalogSHA256, byID: byID}, nil
}

func NewBlockCatalog(version string, definitions []BlockDefinition) (*BlockCatalog, error) {
	owned := append([]BlockDefinition(nil), definitions...)
	sort.Slice(owned, func(i, j int) bool { return owned[i].BlockID < owned[j].BlockID })
	document := blockCatalogDocument{SchemaVersion: machinecontract.SchemaVersion, CatalogVersion: version, Blocks: owned, CatalogSHA256: "sha256:" + strings.Repeat("0", 64)}
	contents, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	digest, err := machinecontract.DigestWithoutTopLevelField(contents, "catalog_sha256")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BlockDefinition, len(owned))
	for _, block := range owned {
		if block.BlockID == "" || (block.Surface != "admin" && block.Surface != "client") || (block.Readiness != "not_ready" && block.Readiness != "ready" && block.Readiness != "deprecated") {
			return nil, fmt.Errorf("invalid feature block definition %q", block.BlockID)
		}
		if _, duplicate := byID[block.BlockID]; duplicate {
			return nil, fmt.Errorf("duplicate feature block %q", block.BlockID)
		}
		byID[block.BlockID] = block
	}
	return &BlockCatalog{version: version, checksum: digest, byID: byID}, nil
}

func (c *BlockCatalog) Version() string  { return c.version }
func (c *BlockCatalog) Checksum() string { return c.checksum }

func (c *BlockCatalog) Validate(blocks []string, surface string) error {
	seen := make(map[string]struct{}, len(blocks))
	for _, blockID := range blocks {
		if _, duplicate := seen[blockID]; duplicate {
			return fmt.Errorf("duplicate feature block %q", blockID)
		}
		seen[blockID] = struct{}{}
		definition, exists := c.byID[blockID]
		if !exists || definition.Surface != surface {
			return fmt.Errorf("%w: %s/%s", ErrUnknownBlock, surface, blockID)
		}
		if definition.Readiness != "ready" {
			return fmt.Errorf("%w: %s/%s is %s", ErrBlockNotReady, surface, blockID, definition.Readiness)
		}
	}
	return nil
}
