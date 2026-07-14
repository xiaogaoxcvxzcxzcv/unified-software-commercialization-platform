package generation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"platform.local/capability-platform/backend/internal/modules/assembly/machinecontract"
)

type PureRenderer struct {
	sources SourceStore
}

func NewPureRenderer(sources SourceStore) *PureRenderer {
	return &PureRenderer{sources: sources}
}

func (r *PureRenderer) Render(ctx context.Context, input Input) (Result, error) {
	if r == nil || r.sources == nil || len(input.Blueprint) == 0 || len(input.Plan) == 0 {
		return Result{}, ErrInvalidInput
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	var plan Plan
	if err := json.Unmarshal(input.Plan, &plan); err != nil {
		return Result{}, fmt.Errorf("%w: plan JSON", ErrInvalidInput)
	}
	if err := validateRequestPlan(input.Request, plan); err != nil {
		return Result{}, err
	}
	contextValue, err := buildTemplateContext(input.Blueprint, input.Plan)
	if err != nil {
		return Result{}, err
	}
	manifestChecksums, err := dependencyChecksums(plan)
	if err != nil {
		return Result{}, err
	}
	outputs := append([]OutputSpec(nil), input.Request.DesiredOutputs...)
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })
	files := make([]RenderedFile, 0, len(outputs))
	for _, output := range outputs {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		default:
		}
		manifestChecksum, ok := manifestChecksums[output.SourceID+"\x00"+output.SourceVersion]
		if !ok {
			return Result{}, fmt.Errorf("%w: source identity is not in plan", ErrPlanMismatch)
		}
		source, err := r.sources.ReadLockedSource(output.SourceID, output.SourceVersion, manifestChecksum, output.SourcePath, output.SourceSHA256)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %s", ErrSourceUnavailable, output.Path)
		}
		if !digestEqual(digestBytes(source), output.SourceSHA256) {
			return Result{}, fmt.Errorf("%w: %s", ErrSourceChecksum, output.Path)
		}
		rendered, err := renderSource(source, output, contextValue)
		if err != nil {
			return Result{}, fmt.Errorf("render %s: %w", output.Path, err)
		}
		digest := digestBytes(rendered)
		files = append(files, RenderedFile{
			OutputSpec: output, Bytes: rendered, SHA256: digest, GeneratedSHA256: digest,
			SourceManifestSHA256: manifestChecksum,
		})
	}
	return Result{Files: files}, nil
}

func validateRequestPlan(request Request, plan Plan) error {
	if request.SchemaVersion != "1.0.0" || request.RequestID == "" || request.WorkspaceRef == "" || request.Operation == "" ||
		!digestEqual(request.PlanChecksum, plan.PlanChecksum) || request.Generator != plan.Generator ||
		!outputSetsEqual(request.DesiredOutputs, plan.ExpectedOutputs) || !secretSetsEqual(request.SecretRefs, plan.RequiredSecretRefs) {
		return ErrPlanMismatch
	}
	if len(request.DesiredOutputs) == 0 {
		return ErrInvalidInput
	}
	paths := make([]string, 0, len(request.DesiredOutputs))
	for _, output := range request.DesiredOutputs {
		if err := validateOutput(output); err != nil {
			return err
		}
		paths = append(paths, strings.ToLower(output.Path))
	}
	sort.Strings(paths)
	for index := 1; index < len(paths); index++ {
		if paths[index] == paths[index-1] || strings.HasPrefix(paths[index], paths[index-1]+"/") {
			return ErrDuplicateOutput
		}
	}
	return nil
}

func validateOutput(output OutputSpec) error {
	if err := machinecontract.ValidateSafeRelativePath(output.Path); err != nil {
		return err
	}
	if err := machinecontract.ValidateSafeRelativePath(output.SourcePath); err != nil {
		return err
	}
	if output.Ownership != "generated" && output.Ownership != "integration" {
		return ErrInvalidInput
	}
	if output.SourceID == "" || output.SourceVersion == "" || !validDigest(output.SourceSHA256) {
		return ErrInvalidInput
	}
	if output.Ownership == "integration" {
		if output.RenderStrategy != "generated_region" || output.ContentType != "text" || output.Merge == nil ||
			output.Merge.Strategy != "generated_region_v1" || output.Merge.RegionID == "" ||
			(output.Merge.CommentPrefix != "//" && output.Merge.CommentPrefix != "#") {
			return ErrInvalidInput
		}
	} else if output.Merge != nil {
		return ErrInvalidInput
	}
	if output.ContentType == "binary" && output.RenderStrategy != "copy" {
		return ErrInvalidInput
	}
	return nil
}

func dependencyChecksums(plan Plan) (map[string]string, error) {
	values := make(map[string]string, len(plan.Packages)+len(plan.Applications))
	for _, item := range plan.Packages {
		key := item.PackageID + "\x00" + item.Version
		if item.PackageID == "" || item.Version == "" || !validDigest(item.Checksum) || values[key] != "" {
			return nil, ErrPlanMismatch
		}
		values[key] = item.Checksum
	}
	for _, application := range plan.Applications {
		item := application.Template
		key := item.TemplateID + "\x00" + item.Version
		if item.TemplateID == "" || item.Version == "" || !validDigest(item.Checksum) {
			return nil, ErrPlanMismatch
		}
		if existing := values[key]; existing != "" && !digestEqual(existing, item.Checksum) {
			return nil, ErrPlanMismatch
		}
		values[key] = item.Checksum
	}
	return values, nil
}

func renderSource(source []byte, output OutputSpec, values any) ([]byte, error) {
	if output.ContentType != "binary" {
		if !utf8.Valid(source) || bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) {
			return nil, ErrTemplateInvalid
		}
		source = normalizeText(source)
	}
	var rendered []byte
	var err error
	switch output.RenderStrategy {
	case "copy":
		rendered = append([]byte(nil), source...)
	case "strict_template", "generated_region":
		rendered, err = renderStrictTemplate(source, values)
	default:
		return nil, ErrUnsupportedRender
	}
	if err != nil {
		return nil, err
	}
	if output.ContentType == "json" {
		rendered, err = machinecontract.Canonicalize(rendered)
		if err != nil {
			return nil, ErrTemplateInvalid
		}
	}
	return rendered, nil
}

func normalizeText(value []byte) []byte {
	value = bytes.ReplaceAll(value, []byte("\r\n"), []byte("\n"))
	value = bytes.ReplaceAll(value, []byte("\r"), []byte("\n"))
	return value
}

func buildTemplateContext(blueprint, plan json.RawMessage) (any, error) {
	var blueprintValue, planValue any
	if err := decodeJSON(blueprint, &blueprintValue); err != nil {
		return nil, ErrInvalidInput
	}
	if err := decodeJSON(plan, &planValue); err != nil {
		return nil, ErrInvalidInput
	}
	return map[string]any{"blueprint": blueprintValue, "plan": planValue}, nil
}

func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func digestEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(strings.ToLower(left)), []byte(strings.ToLower(right))) == 1
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func outputSetsEqual(left, right []OutputSpec) bool {
	if len(left) != len(right) {
		return false
	}
	leftRaw, leftErr := canonicalSortedOutputs(left)
	rightRaw, rightErr := canonicalSortedOutputs(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func canonicalSortedOutputs(values []OutputSpec) ([]byte, error) {
	copyValues := append([]OutputSpec(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i].Path < copyValues[j].Path })
	raw, err := json.Marshal(copyValues)
	if err != nil {
		return nil, err
	}
	return machinecontract.Canonicalize(raw)
}

func secretSetsEqual(left, right []SecretRef) bool {
	if len(left) != len(right) {
		return false
	}
	key := func(value SecretRef) string { return value.Provider + "\x00" + value.Key + "\x00" + value.Environment }
	leftCopy, rightCopy := append([]SecretRef(nil), left...), append([]SecretRef(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return key(leftCopy[i]) < key(leftCopy[j]) })
	sort.Slice(rightCopy, func(i, j int) bool { return key(rightCopy[i]) < key(rightCopy[j]) })
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}
