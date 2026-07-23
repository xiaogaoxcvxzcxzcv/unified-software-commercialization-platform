# G2C-02 experimental access and local runtime checkpoint

Status: HTTP preflight checkpoint, not verified

Date: 2026-07-23

## Scope

This checkpoint covers the local acceptance precondition for G2C-02: a specific active administrator must receive explicit server-side permission to use the isolated experimental assembly catalog. It does not mark G2C-02 verified and does not replace the required browser creation flow for software A.

## Evidence

- Current strict gate remains G2C-02 in `docs/end-to-end-development-plan.md`.
- PR #14 required checks are green for the latest pushed branch state:
  - `quality-gate`: pass
  - `windows-tls`: pass
- Local API check through `https://127.0.0.1:5174` confirmed:
  - administrator login returns 200
  - ordinary catalog route returns 200
  - ordinary packages/templates are empty for the G2C-02 test target
  - experimental catalog route returns 403 before explicit grant
- Added local-only grant command:
  - `platform/backend/cmd/grant-g2c02-experimental-access`
  - requires `--acceptance-g2c02`
  - accepts only an existing active administrator with an existing authorization version
  - requires `PLATFORM_ENVIRONMENT=local`
  - requires a loopback PostgreSQL URL for `platform_local` or isolated acceptance database `platform_g2c02_acceptance`
  - creates a separate `g2c02_experimental_operator` platform binding for `assembly.experimental.use`
  - does not add experimental permission to bootstrap `super_admin`

## Validation run

```text
go test -count=1 ./cmd/grant-g2c02-experimental-access ./internal/modules/assembly/generation ./internal/workflows/assemblyexecution ./cmd/server
```

Result: passed.

Additional focused validation after the route and authorization fixes:

```text
go test -count=1 ./cmd/server ./cmd/grant-g2c02-experimental-access ./internal/modules/accesscontrol/... ./internal/modules/assembly/httptransport ./internal/modules/assembly/planning ./internal/modules/assembly/generation
```

Result: passed.

## Local runtime result

`platform_local` still has older local migration state, so G2C-02 preflight used the isolated local acceptance database `platform_g2c02_acceptance`. The backend restarted successfully against that database with `platform/backend/scripts/admin-local-runtime.ps1 restart -Database platform_g2c02_acceptance`.

After granting the active acceptance administrator the separate `g2c02_experimental_operator` binding, HTTP preflight created software A from the same API path used by the browser flow:

1. login through `https://127.0.0.1:5174`;
2. verify the session authorization snapshot contains `assembly.experimental.use`;
3. verify ordinary catalog remains empty for the test target;
4. verify experimental catalog exposes the candidate package/template/tool combination;
5. create a Product Blueprint for `video-brain` with `package.account`, `package.entitlement`, `standard-a`, `platform.generator`, `platform.sdk`, and `extension.editor-tools`;
6. create the experimental plan through `/api/v1/admin/experimental/blueprints/{blueprint_id}/plan`;
7. start assembly with server-controlled `output_target_ref=workspace.g2c02.a`;
8. poll the run to `completed`;
9. verify Manifest and lock endpoints return 200;
10. verify generated source includes root `AGENTS.md`, `docs/software-development-handoff.md`, `package.json`, and `apps/web/src`.

Report: `artifacts/reviews/G2C-02/g2c02-http-preflight-software-a-20260723232213.json`.

Additional non-browser safety and recovery checkpoints:

- `artifacts/reviews/G2C-02/g2c02-http-safety-recovery-20260723234705.json` records blueprint/plan/run idempotency replays, rejected unknown `output_target_ref`, recovery by a fresh login session, and non-overlapping output roots. The run intentionally remains a failure-recovery checkpoint because the isolated test database already contains the locked `video-brain` product required by the current experimental extension.
- `artifacts/reviews/G2C-02/g2c02-generated-handoff-root-closure.json` records the completed `video-brain` generated source root, artifact root separation, root `AGENTS.md`, `docs/software-development-handoff.md`, `package.json`, `apps/web/src`, and generated/custom/API-SDK boundary wording.
- Browser automation is still unavailable in the current Codex environment: the browser runtime initializes, but browser discovery returns an empty list. Therefore these HTTP and filesystem checkpoints are not final browser-click evidence.

## Remaining G2C-02 evidence required

- Fresh PostgreSQL-backed browser acceptance for software A.
- Successful-run repeated-submit/idempotency evidence from the browser path.
- Browser rejected unknown or mismatched `output_target_ref` evidence.
- Browser recovery evidence after refresh or interrupted response.
- Browser evidence that the completed run automatically enters the new Product workspace.
