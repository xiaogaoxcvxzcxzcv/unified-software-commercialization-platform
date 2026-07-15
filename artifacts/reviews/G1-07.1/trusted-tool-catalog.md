# G1-07.1 Trusted Generator/SDK Tool Catalog

Status: verified

Date: 2026-07-15

## Delivered

- Added the versioned `tool-manifest` machine contract and positive/negative fixtures.
- Added separate ordinary and experimental Generator/SDK roots under `platform/tools/` and `platform/experimental/tools/`.
- Added server-owned directory loading, strict identity/version checks, manifest and content-tree hashing, extra-file/link/reparse protection, execution-entrypoint sealing, platform contract compatibility, and evidence coverage validation.
- Restricted builtin execution to the server registry: `assembly.pure-renderer` for generators and `assembly.client-sdk` for SDKs.
- Extended Catalog Snapshot with `catalog_scope`, generators, and SDKs; snapshot bytes and checksum are deterministic.
- Removed the planning package's in-memory trusted-tool catalog. The server loads tool roots from configuration, and the planner resolves the exact Generator and SDK against every application target, delivery mode, and environment.
- Product Blueprint now requires at least one capability package and still accepts only tool ID/version, never paths, commands, scope, or checksums.
- Assembly Manifest now records the resolved Generator ID, version, and checksum.

## Rejection Evidence

Automated tests reject:

- unknown tool IDs and versions;
- ordinary/experimental scope or readiness mixing;
- unregistered builtin adapters;
- unsafe external entrypoint paths;
- entrypoint and evidence files not sealed by `content_files`;
- checksum/content-tree drift, undeclared files, case collisions, links, junctions, and reparse points through the shared disk-integrity validator;
- unsupported target, delivery mode, or environment for any application;
- a Product Blueprint with no capability package.

## Verification

- Focused machine catalog, planner, generator artifact, machine contract, and configuration tests passed.
- Full quality gate passed 18/18 with real PostgreSQL, including Go test/vet, schemas/fixtures, machine catalogs, OpenAPI, UTF-8, migration pairing, documentation links, secret scan, SDK/UI tests and builds, Standard-A smoke, and admin tests/build.
- Report: `quality-gate-full-postgres.json`.

## Boundary

The four trusted roots intentionally contain only README files. No real tool version is promoted in this gate because executable Generator/SDK delivery requires the later G2C sample-assembly evidence. Therefore no real creation combination is executable yet, and no capability package is marked `available`.

The next and only gate is G1-08.1, the create-software API Client and state model.
