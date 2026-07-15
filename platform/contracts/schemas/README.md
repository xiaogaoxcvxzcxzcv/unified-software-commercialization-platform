# Machine Contract Schemas

`v1/` contains the runtime machine contracts for capability packages, UI templates, product blueprints, assembly records, generator input/output, project locks and product extensions. Markdown explains intent; these JSON Schemas are the runtime validation source of truth.

All contracts use JSON Schema Draft 2020-12 and require `schema_version: 1.0.0`. Objects are closed with `additionalProperties: false`. Secrets are represented only by `secret_ref`; generated file locations use the shared safe relative path definition.

The backend package `internal/modules/assembly/machinecontract`:

- compiles every schema with ECMA-262 regular-expression semantics;
- validates all files in `fixtures/` against their owning schema;
- canonicalizes documents with RFC 8785 JCS and computes SHA-256 digests;
- rejects unsafe cross-platform generated paths.

`package-manifest` and `ui-template-manifest` calculate `manifest_sha256` from the RFC 8785 canonical JSON document after removing the top-level `manifest_sha256` member. `content_tree_sha256` covers the stable, path-sorted `content_files` projection and every listed file is verified from raw bytes. Stored digests include the `sha256:` prefix.

Fixture conventions:

- `fixtures/catalog-blueprint/<schema>.<case>.json`
- `fixtures/assembly-generator/<schema>/<case>.json`
- names ending in `.valid.json`, or beginning with `valid` in a schema directory, must pass;
- all other fixture cases must be rejected.

Run the focused validation from `platform/backend`:

```powershell
go test -count=1 ./internal/modules/assembly/machinecontract
```

The shared Full quality gate runs this as a separate blocking step before the complete Go suite.

G1-02 adds `catalog-snapshot` and `feature-block-catalog`, bringing the current suite to 14 schemas and 57 fixtures. The package/template resolver has its own focused gate:

```powershell
go test -count=1 ./internal/modules/assembly/machinecatalog
```

Production ordinary and experimental catalog paths are documented in `platform/contracts/README.md`. Empty production catalogs are valid and mean that no complete capability package or UI template is currently selectable.
