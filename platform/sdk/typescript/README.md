# TypeScript Client SDK

`@capability-platform/client-sdk` establishes a trusted Product/Application/Tenant context and provides the stable `sdk.account` and `sdk.entitlement` composition entries.

## Trusted client context

Call `sdk.establishSession(...)` before Client Session operations. The SDK keeps the short-lived Client Session bearer in memory and does not let callers set Product, Tenant, Application, Authorization, Cookie, or Client Session headers. Existing `sdk.request()` behavior remains available for versioned Client API calls.

## Account

The `sdk.account` API implements:

- `startRegistrationVerification`, `registerUser`, `login`, `getCurrentSession`, `refreshSession`, `restoreSession`, `clearSession`, and `logout`
- `startRecovery` and `completeRecovery`
- `startExternalLogin`, `completeExternalLogin`, and `exchangeWechatCode`
- `getProfile`, `updateProfile`, and `changePassword`
- `listSessions`, `revokeSession`, `listExternalIdentities`, `linkExternalIdentity`, `unlinkExternalIdentity`, and `getAccessSummary`

Registration, login, recovery, and external authentication use the established Client Session. Profile, security, session, external identity, and access methods use the SDK-held User Session. External exchange creates a User Session only for a strict `authenticated` result; link, conflict, review, and unknown results never become authentication. Refresh sends only the current refresh token and one stable `client_request_id`; no Account method accepts a bearer, Cookie, or caller-selected scope ID.

Credential responses must carry `Cache-Control: no-store`. Required response fields are parsed strictly, unknown fields are ignored, and future enum values become `unknown`. Account errors preserve stable code/kind/request metadata while hiding server detail that could echo credentials or identifiers.

    import { ClientSdk } from "@capability-platform/client-sdk";

    const sdk = new ClientSdk({ baseUrl: "https://api.example.com" });
    await sdk.establishSession(clientSessionInput);
    const verification = await sdk.account.startRegistrationVerification(
      { identifier: "user@example.com" },
      { idempotencyKey: crypto.randomUUID() },
    );
    await sdk.account.registerUser(
      {
        identifier: "user@example.com",
        credential: password,
        verificationContinuationId: verification.continuationId,
        verificationProof,
      },
      { idempotencyKey: crypto.randomUUID() },
    );

    const profile = await sdk.account.getProfile();
    await sdk.account.updateProfile(
      { expectedVersion: profile.version, displayName: "Ada" },
      { idempotencyKey: crypto.randomUUID() },
    );
    await sdk.account.logout();

## Session storage and retries

Access and refresh tokens stay in memory by default. A desktop host may inject an `AccountSessionVault` backed by operating-system secure storage through `accountSessionVault`. This package deliberately provides no Web Storage, plaintext file, or fixed-key Vault implementation. `restoreSession()` rejects malformed or refresh-expired records and refreshes an access-expired record with one recovery request ID.

Login, password, and one-time external authentication attempts are never retried. Keyed registration, verification, recovery, profile, and external-link writes, refresh with the same request ID, and safe reads use the configured bounded retry limit. Idempotency keys and refresh `client_request_id` values must be 16 to 128 characters and are validated before any retryable request starts. An indeterminate refresh keeps the same pending request ID in memory and in the injected Vault until a later call succeeds or receives a definitive terminal response. Network failures, timeouts, and caller cancellation preserve a still-valid session. Logout success, revocation of the known current session, and stable terminal identity/session errors clear memory and the injected Vault.

## Entitlement

The `sdk.entitlement` API implements:

- `checkEntitlement`
- `getCurrentEntitlements`
- `listEntitlementHistory`

Entitlement calls require both the established Client Session and the SDK-held Account User Session. Callers provide only requested feature codes, optional device diagnostics, and pagination hints. The SDK does not accept caller-selected Product/Tenant/User IDs, bearer tokens, Cookies, prices, plan marketing text, or client-computed authorization results.

    await sdk.account.login({ identifier: "user@example.com", credential: password });
    const decision = await sdk.entitlement.checkEntitlement({
      requestedFeatures: ["export_pdf"],
      deviceId: "device-1",
      clientTime: new Date().toISOString(),
    });
    const current = await sdk.entitlement.getCurrentEntitlements();
    const history = await sdk.entitlement.listEntitlementHistory({ pageSize: 50 });

`getCurrentEntitlements()` preserves the current OpenAPI summary fields: `revision`, `planCode`, `features`, `validUntil`, `offlineGraceUntil`, and `updatedAt`. `updatedAt` is only a display/cache-boundary hint. Client caches may use this data only as a short-lived UI hint; `ENTITLEMENT_EXPIRED`, `ENTITLEMENT_REQUIRED`, `ENTITLEMENT_CAPABILITY_DISABLED`, revocation, and capability-disable responses must be treated as authoritative server results. History is append-only evidence and must not be converted into current authorization.
