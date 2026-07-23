# G2C-01 candidate catalog compatibility verification

Status: verified

Date: 2026-07-23

## Scope

G2C-01 locks the experimental candidate inputs needed for the first account + entitlement assembly plan:

- `package.account` 1.0.0, experimental verified candidate.
- `package.entitlement` 1.0.0, experimental verified candidate.
- `standard-a` 0.1.0, experimental verified template now declaring account + entitlement package compatibility and public client blocks.
- `platform.generator` 1.0.0, experimental verified tool manifest using the registered `assembly.pure-renderer` builtin adapter.
- `platform.sdk` 1.0.0, experimental verified tool manifest using the registered `assembly.client-sdk` builtin adapter.
- `extension.editor-tools` 1.0.0, experimental verified sample Extension Manifest for `video-brain`.

## Local validation

- `go test -count=1 ./internal/modules/assembly/machinecatalog ./internal/modules/assembly/planning` passed.
- `scripts/quality-gate.ps1 -Mode Core -ReportPath artifacts/reviews/G2C-01/quality-gate-core-candidate-catalog.json` passed 6/6.
- `scripts/quality-gate.ps1 -Mode Full -RequirePostgres -ReportPath artifacts/reviews/G2C-01/quality-gate-full-postgres.json` passed 22/22.

## Coverage

- Real experimental catalog loads packages, template, generator, SDK, and extension from source-controlled directories.
- Selecting `package.entitlement` resolves the `package.account` dependency and locks a deterministic catalog snapshot.
- Planner builds deterministic Assembly Plan bytes for the real experimental account + entitlement + standard-a + tool + extension combination.
- Ordinary catalog options remain empty and cannot see the experimental candidate closure.
- The first failed Full attempt used the wrong local DSN user (`platform_test_user`) and failed PostgreSQL authentication before exercising module logic. The verification run was repeated with the repository-defined local user (`platform_test`) and passed.

## Remote CI

The code checkpoint was published to PR #14 by updating the remote branch to commit `8509c3e76e46588430a017a3045da18e757baad1`; that commit's push and pull_request `quality-gate` / `windows-tls` checks passed.

The status-sync commit `6cc9909` was pushed to PR #14 and both push run `30006697515` and pull_request run `30006701403` passed `quality-gate` and `windows-tls`.

G2C-01 is verified. The next unique gate is G2C-02: real creation of assembly acceptance software A through the controlled experimental entry.
