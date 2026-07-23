# G2A-04 External Identity And Security Notification Evidence

Date: 2026-07-17

Status: verified; local and hosted push/PR quality gates complete.

## Scope

- ADR-0017 freezes Identity, Product Application, and Notification ownership.
- OpenAPI exposes registration verification, external login start/callback, WeChat one-time exchange, link/list/unlink, and stable redacted responses.
- Migration `000017` adds exact auth return targets, external auth flow/proof/registration challenge state, external session provenance, and durable security delivery/outbox state.
- The server composition keeps external providers disabled unless a production adapter is explicitly configured. No test provider is presented as a real OIDC or WeChat integration.

## Security Properties Verified

- Client session, Product, Application, Tenant, environment, provider, browser session, return target, state, nonce, PKCE, flow lease, and provider code are server-bound.
- Provider codes, proofs, tokens, destinations, AppSecret values, payload keys, and digest keys are not returned or persisted in plaintext.
- External flow and proof consumption are single-use; cross-flow code replay, cross-client scope, cross-browser session, provider mismatch, provider-application mismatch, expiry, and abandoned processing leases fail closed.
- External identity link is idempotent, subject identity is provider-application scoped, and unlink revokes only sessions created by the exact external identity.
- User-level locking prevents concurrent unlink operations from removing the final login method. Login completion and unlink use the same `user -> external_identity` lock order.
- Registration verification and recovery remain pending until encrypted delivery is durably accepted; failed delivery leaves no active proof. Registration consumption can recover the same request after a downstream transaction failure.
- Security delivery payloads use AEAD with delivery, purpose, trusted scope, provider, destination type, expiry, and trace context as authenticated data.
- Provider capability preflight and delivery use one gateway; enabled providers must guarantee `delivery_id` idempotency.
- Delivery and outbox claims use database time and formal leases. Crashed delivery attempts are immutable, use the database `lease_started_at`, and become terminal at the configured maximum.
- Delivery facts, attempt facts, outbox facts, and terminal states are protected by PostgreSQL constraints/triggers against mutation, invalid transition, attempt skipping, and deletion.
- Product Application redirect outbox uses random lease tokens and database-time CAS completion; an expired worker cannot publish or fail a successor claim.

## Review Findings Closed

- HTTP registration composition originally omitted `verification_continuation_id`; mapping and regression coverage now preserve continuation, proof, scope, idempotency key, and trace.
- WeChat exchange originally drifted from OpenAPI; it now uses an explicit one-time-flow idempotency exemption and accepts only `code`, while generic callback may carry a stable provider error.
- Register OpenAPI accidentally inherited the WeChat exemption; route-specific validation now requires `Idempotency-Key` and forbids the exemption.
- Notification no-work maintenance originally rolled back recovered dead state; maintenance is committed before returning `not found`.
- Notification tests originally queried an arbitrary attempt and manufactured impossible database states; assertions now select the current attempt and use real short lease expiry.
- The first PR quality gate exposed a cached test-start database clock in the short-TTL expiry case. The test now reads `clock_timestamp()` immediately before creating a two-second delivery, then waits for natural expiry; the focused case passed 10 repeated runs and local Full passed again.
- Concurrent unlink, delivery/outbox immutable state, Product Application stale workers, lock order, delete protection, and database-authoritative attempt time were added after adversarial read-only reviews.

## Local Verification

- OpenAPI: 91 paths, 97 operations, 97 unique operation IDs.
- Strict UTF-8: 630 text files.
- Migration naming/pairing: 17 versions.
- Local Markdown links: 128 files.
- Secret pattern scan: passed without reporting matched values.
- Real PostgreSQL migration, Identity, Notification, and Product Application suites: passed.
- Concurrent final-login-method unlink: 10 repeated PostgreSQL runs passed.
- Notification PostgreSQL concurrency, lease recovery, retry, dead letter, immutable facts, and outbox: 10 repeated runs passed.
- Product Application stale lease token test: 10 repeated PostgreSQL runs passed.
- Full quality gate: 18/18 passed with `-RequirePostgres`.
- SDK: 8/8; Client UI: 14/14; Standard-A web and desktop WebView: 7/7 each; Admin: 133/133 plus production build.
- Machine report: `artifacts/reviews/G2A-04/quality-gate-full.json`.

## Explicit Deferrals

- No production OIDC or WeChat application credentials are available. Those entries remain disabled; no real-provider E2E is claimed.
- Browser GET callback, interaction restoration, and one-time browser interaction code belong to G2A-04.1. G2A-04 validates the authenticated server JSON POST exchange boundary and must not fabricate browser screenshots.
- ST-022 records only server replay, scope, provider-application, conflict, and unlink safety subranges. The real WeChat-provider E2E remains unverified.
- ST-025 records only exact named return-target and server callback/PKCE subranges. The HostedInteraction browser flow remains unverified.
- `package.account` remains `contracted`; no package is `verified candidate` or `available` at this gate.

## Hosted Verification

Draft PR #13 remains open and unmerged. Initial push run `29585535288` passed; initial PR run `29585568077` exposed and invalidated the cached-clock short-TTL test. After remediation commit `303d69a0ebc795a59d4540d8c1bb26eddd2a1ad1`, fresh push run `29586445175` and PR run `29586447148` both passed `quality-gate`. This evidence supports G2A-04 `verified`; G2A-04.1 is now the only strict gate and remains `planned` until its own evidence is complete.
