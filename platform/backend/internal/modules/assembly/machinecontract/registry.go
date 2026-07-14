package machinecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	SchemaVersion = "1.0.0"
	draft2020URL  = "https://json-schema.org/draft/2020-12/schema"
)

var ErrUnknownSchema = errors.New("unknown machine contract schema")

type Registry struct {
	schemas  map[string]*jsonschema.Schema
	checksum string
}

func LoadDirectory(directory string) (*Registry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read machine contract schemas: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseRegexpEngine(compileECMAScriptRegexp)

	locations := map[string]string{}
	type schemaDigestEntry struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}
	digestEntries := make([]schemaDigestEntry, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read schema %s: %w", entry.Name(), err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(contents))
		if err != nil {
			return nil, fmt.Errorf("parse schema %s: %w", entry.Name(), err)
		}
		object, ok := document.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema %s must be a JSON object", entry.Name())
		}
		if object["$schema"] != draft2020URL {
			return nil, fmt.Errorf("schema %s must declare JSON Schema Draft 2020-12", entry.Name())
		}
		id, ok := object["$id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("schema %s must declare a non-empty $id", entry.Name())
		}
		name := strings.TrimSuffix(entry.Name(), ".schema.json")
		if _, exists := locations[name]; exists {
			return nil, fmt.Errorf("duplicate schema name %q", name)
		}
		if err := compiler.AddResource(id, document); err != nil {
			return nil, fmt.Errorf("register schema %s: %w", entry.Name(), err)
		}
		locations[name] = id
		digest, err := Digest(contents)
		if err != nil {
			return nil, fmt.Errorf("digest schema %s: %w", entry.Name(), err)
		}
		digestEntries = append(digestEntries, schemaDigestEntry{Name: name, Digest: "sha256:" + digest})
	}
	if len(locations) == 0 {
		return nil, errors.New("machine contract schema directory is empty")
	}

	sort.Slice(digestEntries, func(i, j int) bool { return digestEntries[i].Name < digestEntries[j].Name })
	digestDocument, err := json.Marshal(digestEntries)
	if err != nil {
		return nil, fmt.Errorf("marshal schema catalog digest: %w", err)
	}
	catalogDigest, err := Digest(digestDocument)
	if err != nil {
		return nil, fmt.Errorf("digest schema catalog: %w", err)
	}
	registry := &Registry{schemas: make(map[string]*jsonschema.Schema, len(locations)), checksum: "sha256:" + catalogDigest}
	for name, location := range locations {
		schema, err := compiler.Compile(location)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", name, err)
		}
		registry.schemas[name] = schema
	}
	return registry, nil
}

func (r *Registry) Version() string { return SchemaVersion }

func (r *Registry) Checksum() string { return r.checksum }

type ecmaRegexp regexp2.Regexp

func (re *ecmaRegexp) MatchString(value string) bool {
	matched, err := (*regexp2.Regexp)(re).MatchString(value)
	return err == nil && matched
}

func (re *ecmaRegexp) String() string {
	return (*regexp2.Regexp)(re).String()
}

func compileECMAScriptRegexp(expression string) (jsonschema.Regexp, error) {
	re, err := regexp2.Compile(expression, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	return (*ecmaRegexp)(re), nil
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.schemas))
	for name := range r.schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Validate(name string, document []byte) error {
	schema, ok := r.schemas[strings.TrimSuffix(name, ".schema.json")]
	if !ok {
		return ErrUnknownSchema
	}
	canonical, err := Canonicalize(document)
	if err != nil {
		return fmt.Errorf("canonicalize machine contract: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(canonical))
	if err != nil {
		return fmt.Errorf("parse machine contract: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate machine contract %s: %w", name, err)
	}
	return nil
}
