package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

type sourceStoreStub struct {
	contents map[string][]byte
}

func (s sourceStoreStub) ReadLockedSource(sourceID, version, manifestChecksum, sourcePath, sourceChecksum string) ([]byte, error) {
	value, ok := s.contents[sourceID+"\x00"+version+"\x00"+manifestChecksum+"\x00"+sourcePath+"\x00"+sourceChecksum]
	if !ok {
		return nil, errors.New("missing")
	}
	return append([]byte(nil), value...), nil
}

func TestPureRendererIsDeterministicAndUsesExplicitEncoding(t *testing.T) {
	source := []byte("export const productName = {{json:blueprint.product.name}};\r\nexport const productCode = {{identifier:blueprint.product.symbol}};\r\n")
	sourceDigest := rawDigest(source)
	manifestDigest := rawDigest([]byte("manifest"))
	output := OutputSpec{Path: "apps/web/src/generated/product.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0", SourcePath: "content/product.ts.tmpl", SourceSHA256: sourceDigest, RenderStrategy: "strict_template", ContentType: "text"}
	plan := planDocument(t, output, manifestDigest)
	request := requestDocument(output, plan)
	renderer := NewPureRenderer(sourceStoreStub{contents: map[string][]byte{
		output.SourceID + "\x00" + output.SourceVersion + "\x00" + manifestDigest + "\x00" + output.SourcePath + "\x00" + sourceDigest: source,
	}})
	input := Input{Request: request, Blueprint: json.RawMessage(`{"product":{"name":"视频大脑","symbol":"VideoBrain"}}`), Plan: plan}
	first, err := renderer.Render(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := renderer.Render(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 1 || string(first.Files[0].Bytes) != "export const productName = \"视频大脑\";\nexport const productCode = VideoBrain;\n" {
		t.Fatalf("rendered bytes = %q", first.Files[0].Bytes)
	}
	if string(first.Files[0].Bytes) != string(second.Files[0].Bytes) || first.Files[0].SHA256 != second.Files[0].SHA256 {
		t.Fatal("same input produced different generated output")
	}
}

func TestPureRendererRejectsPlanDriftAndUnsafeTemplates(t *testing.T) {
	source := []byte("{{json:blueprint.missing}}")
	output := OutputSpec{Path: "src/generated/value.ts", Ownership: "generated", SourceID: "package.account", SourceVersion: "1.0.0", SourcePath: "content/value.tmpl", SourceSHA256: rawDigest(source), RenderStrategy: "strict_template", ContentType: "text"}
	manifestDigest := rawDigest([]byte("manifest"))
	plan := planDocument(t, output, manifestDigest)
	renderer := NewPureRenderer(sourceStoreStub{contents: map[string][]byte{
		output.SourceID + "\x00" + output.SourceVersion + "\x00" + manifestDigest + "\x00" + output.SourcePath + "\x00" + output.SourceSHA256: source,
	}})
	request := requestDocument(output, plan)
	request.PlanChecksum = rawDigest([]byte("other-plan"))
	_, err := renderer.Render(context.Background(), Input{Request: request, Blueprint: json.RawMessage(`{"product":{}}`), Plan: plan})
	if !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("plan drift error = %v", err)
	}
	request = requestDocument(output, plan)
	_, err = renderer.Render(context.Background(), Input{Request: request, Blueprint: json.RawMessage(`{"product":{}}`), Plan: plan})
	if !errors.Is(err, ErrTemplateValue) {
		t.Fatalf("template value error = %v", err)
	}
}

func planDocument(t *testing.T, output OutputSpec, manifestDigest string) json.RawMessage {
	t.Helper()
	value := map[string]any{
		"plan_checksum":    rawDigest([]byte("plan")),
		"generator":        map[string]any{"generator_id": "platform.generator", "version": "1.0.0", "checksum": rawDigest([]byte("generator"))},
		"expected_outputs": []OutputSpec{output}, "required_secret_refs": []any{},
		"packages":     []any{map[string]any{"package_id": output.SourceID, "version": output.SourceVersion, "checksum": manifestDigest}},
		"applications": []any{},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func requestDocument(output OutputSpec, plan json.RawMessage) Request {
	var locked Plan
	_ = json.Unmarshal(plan, &locked)
	return Request{SchemaVersion: "1.0.0", RequestID: "request.test", Operation: "generate", WorkspaceRef: "workspace.test", PlanChecksum: locked.PlanChecksum, TargetSnapshotChecksum: rawDigest(nil), Generator: locked.Generator, DesiredOutputs: []OutputSpec{output}, SecretRefs: []SecretRef{}}
}

func rawDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
