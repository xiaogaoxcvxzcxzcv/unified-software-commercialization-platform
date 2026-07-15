package generation

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestBuildRequestUsesTrustedRuntimeIdentityAndCurrentSnapshot(t *testing.T) {
	target := t.TempDir()
	writeTestFile(t, target, "src/custom/workbench.ts", []byte("custom\n"))
	output := artifactTestOutput("src/generated/account.ts")
	planRaw, err := json.Marshal(map[string]any{
		"plan_id": "plan.request-factory", "plan_checksum": rawDigest([]byte("plan")),
		"blueprint_id": "bp_test-product", "blueprint_version": 1,
		"catalog_snapshot": map[string]any{"checksum": rawDigest([]byte("catalog"))},
		"generator":        map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": rawDigest([]byte("generator"))},
		"expected_outputs": []OutputSpec{output}, "required_secret_refs": []SecretRef{},
		"packages":     []any{map[string]any{"package_id": "package.account", "version": "1.0.0", "checksum": rawDigest([]byte("package"))}},
		"applications": []any{map[string]any{"application_id": "application.web", "template": map[string]any{"template_id": "standard-web", "version": "1.0.0", "checksum": rawDigest([]byte("template"))}}},
		"sdks":         []any{map[string]any{"sdk_id": "sdk.typescript", "version": "1.0.0", "checksum": rawDigest([]byte("sdk"))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	input, previous, err := BuildRequest(target, RequestSpec{
		WorkspaceRef: "workspace.default", RunID: "run.request-factory", RunCreatedAt: createdAt,
		Product:           ArtifactProduct{ProductID: "product.test", OfficialTenantID: "tenant.official", Applications: []ArtifactApplication{{PlanApplicationID: "application.web", ApplicationID: "app.web"}}},
		Blueprint:         ArtifactBlueprint{BlueprintID: "bp_test-product", Version: 1, Checksum: rawDigest([]byte("blueprint"))},
		BlueprintDocument: json.RawMessage(`{"schema_version":"1.0.0"}`), PlanDocument: planRaw,
	})
	if err != nil || previous != (PreviousArtifacts{}) {
		t.Fatalf("BuildRequest() previous=%#v error=%v", previous, err)
	}
	if input.Request.Operation != "generate" || input.Request.TargetSnapshotChecksum == "" || len(input.Request.ProtectedPaths) != 1 || input.Request.ProtectedPaths[0] != "src/custom/workbench.ts" {
		t.Fatalf("request = %#v", input.Request)
	}
	requestRaw, err := json.Marshal(input.Request)
	if err != nil {
		t.Fatal(err)
	}
	if err := artifactTestRegistry(t).Validate("generator-request", requestRaw); err != nil {
		t.Fatal(err)
	}
	evidencePath := input.Request.ArtifactContext.Evidence[0].Path
	if len(input.EvidenceDocuments[evidencePath]) == 0 || digestBytes(input.EvidenceDocuments[evidencePath]) != input.Request.ArtifactContext.Evidence[0].SHA256 {
		t.Fatalf("evidence closure is incomplete: %s", evidencePath)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
}
