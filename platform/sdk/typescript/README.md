# TypeScript Client SDK

The v0.1 SDK establishes a trusted Product/Application/Tenant context from `POST /api/v1/client/session` and keeps the short-lived client session token in memory only. Callers cannot supply Product, Tenant, Application, authorization, or cookie headers through the public request API.

It provides stable classified errors, bounded timeouts, caller cancellation, request IDs, unknown-enum fallback, and at most two retries for client-session establishment, safe methods, or writes carrying an idempotency key. It never treats local cache or caller input as an entitlement, payment, price, or scope fact.
