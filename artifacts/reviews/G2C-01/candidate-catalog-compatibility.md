# G2C-01 candidate catalog compatibility checkpoint

Status: in_progress_checkpoint

Date: 2026-07-23

## Scope

This checkpoint starts G2C-01 by locking the experimental candidate inputs needed for the first account + entitlement assembly plan:

- `package.account` 1.0.0, experimental verified candidate.
- `package.entitlement` 1.0.0, experimental verified candidate.
- `standard-a` 0.1.0, experimental verified template now declaring account + entitlement package compatibility and public client blocks.
- `platform.generator` 1.0.0, experimental verified tool manifest using the registered `assembly.pure-renderer` builtin adapter.
- `platform.sdk` 1.0.0, experimental verified tool manifest using the registered `assembly.client-sdk` builtin adapter.
- `extension.editor-tools` 1.0.0, experimental verified sample Extension Manifest for `video-brain`.

## Local validation

- `go test -count=1 ./internal/modules/assembly/machinecatalog ./internal/modules/assembly/planning` passed.
- `scripts/quality-gate.ps1 -Mode Core -ReportPath artifacts/reviews/G2C-01/quality-gate-core-candidate-catalog.json` passed 6/6.

## Coverage

- Real experimental catalog loads packages, template, generator, SDK, and extension from source-controlled directories.
- Selecting `package.entitlement` resolves the `package.account` dependency and locks a deterministic catalog snapshot.
- Planner builds deterministic Assembly Plan bytes for the real experimental account + entitlement + standard-a + tool + extension combination.
- Ordinary catalog options remain empty and cannot see the experimental candidate closure.

## Not yet verified

This checkpoint does not mark G2C-01 verified. Remaining G2C-01 work still needs the broader ST-027 negative matrix, Full gate, remote CI, final status synchronization, and evidence review.
