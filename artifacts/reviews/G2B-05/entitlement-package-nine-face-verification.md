# G2B-05 Entitlement package nine-face verification

Status: verified

Date: 2026-07-23

## Scope

G2B-05 verifies `package.entitlement` as an experimental `verified candidate` only. It does not publish the package to the ordinary catalog and does not make it available from the ordinary `/create` entry.

## Delivered

- Published `platform/experimental/capability-packages/package.entitlement/1.0.0/manifest.json` with:
  - `lifecycle_status=verified`
  - `visibility=experimental`
  - `readiness=verified`
  - `target=web` and `target=desktop_webview`
  - `delivery_mode=generated_source`
  - `environment=test`
- Kept `platform/contracts/packages/package.entitlement/1.0.0/manifest.json` contracted with `availability=[]`.
- Kept `platform/capability-packages/package.entitlement` absent, so the ordinary catalog cannot expose this package.
- Promoted `entitlement.summary` in the runtime Feature Block Catalog from `not_ready` to `ready` after G2B-04 UI/SDK/source verification and this package-level gate.
- Added MachineCatalog regression tests for ordinary/experimental isolation and content integrity.
- Added a named PostgreSQL ST-039 regression test covering:
  - concurrent duplicate grant replay;
  - idempotency/source tuple deduplication;
  - multiple source stacking;
  - revoking one source without revoking the remaining source;
  - server UTC expiry;
  - cross-product and cross-tenant refusal;
  - ledger/effect counts.

## Nine delivery faces

| Face | Evidence |
|---|---|
| Product result | `docs/features/entitlement/README.md`, `docs/features/entitlement/contract.md`, package manifest |
| User frontend | `entitlement.summary`, Client UI 129/129, Hosted Web 57/57 |
| Unified admin | `entitlement.table`, `entitlement.grant-panel`, `entitlement.history`, Admin 165/165 |
| Backend | `000026_entitlement`, Entitlement Go tests, real PostgreSQL Full gate |
| SDK/channel | SDK Entitlement tests included in Client SDK 43/43 |
| Configuration/Provider | `config.schema.json`, no required provider or secret refs |
| Source delivery | Entitlement generated source templates and generation tests |
| Quality evidence | Targeted Go tests plus Full `-RequirePostgres` 22/22 |
| Documentation | Feature docs, package catalog, smoke tests, implementation status, and this report |

## Local validation

- `go test -count=1 ./internal/modules/assembly/machinecatalog ./internal/modules/assembly/machinecontract ./internal/modules/assembly/generation ./internal/modules/entitlement/...` from `platform/backend`: passed.
- `scripts/quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath artifacts/reviews/G2B-05/quality-gate-full-postgres.json`: passed, 22 steps.
- Quality report: `artifacts/reviews/G2B-05/quality-gate-full-postgres.json`.

## Publication boundary

`package.entitlement` is now eligible only for the controlled G2C experimental path. It is still not ordinary `available`; G2C must still prove real A/B/C assembly, upgrade/rollback, custom safety, and old product regression before ordinary `/create` can expose account + entitlement + standard UI.

## Hosted CI

- Commit: `ff17adf`
- Push run: `30001599641`; `quality-gate` job `89188213209` passed, `windows-tls` job `89187816134` passed.
- Pull request run: `30001604250`; `quality-gate` job `89188239483` passed, `windows-tls` job `89187831418` passed.

## Next gate

G2C-01 must assemble the locked experimental catalog inputs. `package.entitlement` remains invisible to the ordinary catalog until G2C completes A/B/C assembly, upgrade/rollback, custom safety, and old product regression.
