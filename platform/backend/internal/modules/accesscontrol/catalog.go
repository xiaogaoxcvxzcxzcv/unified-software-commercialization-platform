package accesscontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

const PermissionCatalogVersion = "1.6.0"

var (
	ErrInvalidPermissionCatalog = errors.New("invalid permission catalog")
	ErrUnknownPermission        = errors.New("unknown permission")
	permissionCodePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

type PermissionRisk string

const (
	PermissionRiskNormal PermissionRisk = "normal"
	PermissionRiskHigh   PermissionRisk = "high"
)

// PermissionDefinition is platform-owned policy. Capability manifests may
// reference Code, but they cannot add permissions or grant them to a role.
type PermissionDefinition struct {
	Code        string
	Description string
	Risk        PermissionRisk

	grantToBootstrapPlatformSuperAdmin bool
}

type PermissionCatalog struct {
	version     string
	definitions []PermissionDefinition
	byCode      map[string]PermissionDefinition
}

var currentPermissionCatalog = mustPermissionCatalog(
	PermissionCatalogVersion,
	[]PermissionDefinition{
		{Code: "access.manage", Description: "Manage administrator roles, permissions, and scopes", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "assembly.blueprint.manage", Description: "Create and manage product assembly blueprints", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "assembly.execute", Description: "Execute confirmed product assembly plans", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "assembly.experimental.use", Description: "Read and use the isolated experimental software assembly catalog", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: false},
		{Code: "assembly.lifecycle.execute", Description: "Execute, cancel, and roll back assembly lifecycle operations", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "assembly.lifecycle.plan", Description: "Create assembly upgrade and eject lifecycle plans", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "assembly.plan", Description: "Create deterministic product assembly plans", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "assembly.read", Description: "Read product assembly blueprints, plans, runs, manifests, and locks", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "audit.read", Description: "Read security and operation audit events", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "entitlement.manage", Description: "Manage product entitlements", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "entitlement.read", Description: "Read product entitlement summaries and history", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "entitlement.revoke", Description: "Revoke product entitlement grants", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "identity.manage", Description: "Manage user and administrator identity security", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "identity.security.manage", Description: "Manage global user security status and revoke global sessions", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "identity.user.read", Description: "Read redacted end-user identity and session summaries", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "platform.read", Description: "Read platform-level operational information", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "product.application.manage", Description: "Create and manage product applications", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "product.application.security.manage", Description: "Manage application redirects, client bindings, and credentials", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "product.manage", Description: "Create and manage software products and applications", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "product.read", Description: "Read software product information", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "product.user-access.manage", Description: "Manage end-user admission within an authorized product or tenant scope", Risk: PermissionRiskHigh, grantToBootstrapPlatformSuperAdmin: true},
		{Code: "tenant.manage", Description: "Manage product tenant scopes", Risk: PermissionRiskNormal, grantToBootstrapPlatformSuperAdmin: true},
	},
)

func CurrentPermissionCatalog() PermissionCatalog {
	return currentPermissionCatalog
}

func newPermissionCatalog(version string, definitions []PermissionDefinition) (PermissionCatalog, error) {
	if version == "" {
		return PermissionCatalog{}, fmt.Errorf("%w: version is required", ErrInvalidPermissionCatalog)
	}
	if len(definitions) == 0 {
		return PermissionCatalog{}, fmt.Errorf("%w: at least one permission is required", ErrInvalidPermissionCatalog)
	}
	if !sort.SliceIsSorted(definitions, func(i, j int) bool { return definitions[i].Code < definitions[j].Code }) {
		return PermissionCatalog{}, fmt.Errorf("%w: definitions must be sorted by permission code", ErrInvalidPermissionCatalog)
	}

	owned := append([]PermissionDefinition(nil), definitions...)
	byCode := make(map[string]PermissionDefinition, len(owned))
	for _, definition := range owned {
		if !permissionCodePattern.MatchString(definition.Code) {
			return PermissionCatalog{}, fmt.Errorf("%w: invalid permission code %q", ErrInvalidPermissionCatalog, definition.Code)
		}
		if definition.Description == "" {
			return PermissionCatalog{}, fmt.Errorf("%w: permission %q has no description", ErrInvalidPermissionCatalog, definition.Code)
		}
		if definition.Risk != PermissionRiskNormal && definition.Risk != PermissionRiskHigh {
			return PermissionCatalog{}, fmt.Errorf("%w: permission %q has invalid risk %q", ErrInvalidPermissionCatalog, definition.Code, definition.Risk)
		}
		if _, exists := byCode[definition.Code]; exists {
			return PermissionCatalog{}, fmt.Errorf("%w: duplicate permission %q", ErrInvalidPermissionCatalog, definition.Code)
		}
		byCode[definition.Code] = definition
	}

	return PermissionCatalog{version: version, definitions: owned, byCode: byCode}, nil
}

func mustPermissionCatalog(version string, definitions []PermissionDefinition) PermissionCatalog {
	catalog, err := newPermissionCatalog(version, definitions)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c PermissionCatalog) Version() string {
	return c.version
}

func (c PermissionCatalog) Definitions() []PermissionDefinition {
	return append([]PermissionDefinition(nil), c.definitions...)
}

func (c PermissionCatalog) Contains(code string) bool {
	_, exists := c.byCode[code]
	return exists
}

// Checksum locks the platform-owned permission definitions into assembly
// catalog snapshots without exposing any role binding or grant operation.
func (c PermissionCatalog) Checksum() string {
	type checksumDefinition struct {
		Code        string         `json:"code"`
		Description string         `json:"description"`
		Risk        PermissionRisk `json:"risk"`
	}
	definitions := make([]checksumDefinition, 0, len(c.definitions))
	for _, definition := range c.definitions {
		definitions = append(definitions, checksumDefinition{
			Code: definition.Code, Description: definition.Description, Risk: definition.Risk,
		})
	}
	contents, err := json.Marshal(struct {
		Version     string               `json:"version"`
		Definitions []checksumDefinition `json:"definitions"`
	}{Version: c.version, Definitions: definitions})
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (d PermissionDefinition) GrantsPlatformSuperAdminOnBootstrap() bool {
	return d.grantToBootstrapPlatformSuperAdmin
}

// ValidateRequiredPermissions validates declarations only. It deliberately
// returns no role binding or grant that a capability manifest could apply.
func (c PermissionCatalog) ValidateRequiredPermissions(codes []string) error {
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if _, duplicate := seen[code]; duplicate {
			return fmt.Errorf("%w: duplicate required permission %q", ErrInvalidPermissionCatalog, code)
		}
		seen[code] = struct{}{}
		if !c.Contains(code) {
			return fmt.Errorf("%w: %s", ErrUnknownPermission, code)
		}
	}
	return nil
}
