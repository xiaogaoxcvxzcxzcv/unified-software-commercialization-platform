# G2B-01 Entitlement model contract evidence

Status: implemented locally; remote PR required checks pending before marking G2B-01 verified.

## Scope

This evidence covers only the G2B-01 contract gate: Entitlement model, package Manifest, state tables, unique constraints, idempotency and concurrency strategy. It does not implement G2B-02 backend code or migration files.

## Contract decisions

- Data owner: Entitlement owns Feature, Policy, Validity, Grant, Revision, Ledger and Check Decision.
- Scope: every business fact is bound to server-validated `product_id + tenant_id + user_id`.
- Source effects: `admin`, `trial`, `gift`, `order`, `license` with stable `source_id` and `source_effect_id`.
- Effects: `grant`, `extend`, `replace`, `revoke`, `expire`.
- Validity: `fixed_duration`, `fixed_end`, `lifetime`; all expiry decisions use server UTC.
- Stacking: feature union, latest expiry/highest limit, explicit mutual exclusion priority or fail-closed conflict.
- Revoke: source-only by default; conclusion/group/all-user revoke only by explicit policy.
- Ledger: append-only; grant/revision/ledger/outbox commit in one transaction in G2B-02.
- Concurrency: writes serialize on `product_id + tenant_id + user_id`; admin writes require `expected_revision`.
- Idempotency: unique `(product_id, tenant_id, user_id, idempotency_key)` plus request hash; source effect uniqueness on `(product_id, tenant_id, user_id, source_type, source_id, source_effect_id)`.
- Migration boundary: next migration must be `000026_entitlement` because `000025_hosted_self_service_flow` is the current latest migration.

## Files

- `docs/features/entitlement/README.md`
- `docs/features/entitlement/contract.md`
- `platform/contracts/packages/package.entitlement/1.0.0/manifest.json`
- `platform/contracts/packages/package.entitlement/1.0.0/config.schema.json`
- `platform/backend/internal/modules/assembly/machinecontract/entitlement_manifest_test.go`

## Validation results

- Passed: `go test -count=1 ./internal/modules/assembly/machinecontract -run Entitlement` with repository-local `GOCACHE`.
- Passed: `go test -count=1 ./internal/modules/assembly/machinecontract ./internal/modules/assembly/machinecatalog` with repository-local `GOCACHE` and `GOMODCACHE`.
- Passed: `./scripts/quality-gate.ps1 -Mode Core -ReportPath .runtime/G2B-01/quality-gate-core.json`.
- Core report: `.runtime/G2B-01/quality-gate-core.json` (runtime artifact, not committed).

Remote PR required checks are still required before changing the work package to `verified`.

## Non-goals

- No backend migration file is created in this gate.
- No admin Entitlement page is promoted from demo.
- No client Entitlement block, SDK method or generated source is implemented.
- `package.entitlement` remains non-ordinary and non-available.