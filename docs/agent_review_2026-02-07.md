# Repo Review (2026-02-07)

This document tracks a deep-dive code review pass (security, correctness, maintainability, docs) and the changes applied in this workspace.

## P0 Findings (High Risk)

- **TLS verification disabled for EBS HTTP client**: `ebs_fields/ebs_client.go` hard-coded `InsecureSkipVerify: true`, allowing MITM.
- **Silent defaulting of identifiers in non-boundary layers** (violates `AGENTS.md`):
  - Tenant ID defaults in store/service layers (`store/store.go`, `store/migrate.go`, `consumer/*`, `merchant/*`, `apigateway/jwt_auth.go`).
  - Some code paths implicitly rely on `"default"` tenant inside shared components.
- **Broken/dangerous webhook-like behavior**: `consumer/services.go` posts to a hard-coded external URL when `billerForm.to` is empty (SSRF/data exfil risk, also plain HTTP).
- **CORS implementation is incorrect**: `apigateway/main.go` sets `Access-Control-Allow-Origin` to a comma-joined list, which browsers ignore; also no `Vary: Origin` handling.
- **Correctness bug**: `store/store.go:RecordLoginAttempt` attempted to scan 2 columns into 1 destination (would error at runtime).
- **Postgres compatibility bug**: `store/store.go` used `LastInsertId()` for Postgres inserts (unsupported), causing IDs to remain unset.

## Changes Applied

- **EBS TLS is now secure-by-default and configurable at the boundary**:
  - Added `noebs.ebs_insecure_skip_verify` config flag (default `false`).
  - Added `ebs_fields.ConfigureEBSHTTPClient(cfg)` called from `cli/config.go` after config load.
  - Added unit tests to lock in behavior.
- **Removed silent Tenant ID defaults in non-boundary layers**:
  - Store/service/auth now return typed errors on missing tenant rather than defaulting.
  - Added `store/errors.go` + store-layer validation tests.
  - Migrations now require an explicit `defaultTenantID` (`store/migrate.go` and `store/004_backfill.go`).
- **Fixed correctness/runtime issues**:
  - `store/store.go:RecordLoginAttempt` now scans correctly and handles `sql.ErrNoRows` robustly.
  - `store/store.go` inserts that need IDs now use `RETURNING id` on Postgres.
- **Fixed CORS**:
  - `apigateway.NoebsCors` now uses Fiber’s `cors` middleware for correct multi-origin allowlists, and exposes `Authorization`.
- **Removed a dangerous outbound default**:
  - `consumer.BillerHooks` no longer posts to a hard-coded URL; it only posts when `noebs.consumer_biller_hooks_url` is set, requires `https` unless `is_debug=true`, and sends a sanitized payload (no PAN).
- **CI/dev ergonomics**:
  - GitHub Actions now uses the Go version from `go.mod` and runs `go test` + `-race`.
  - `make test` now runs `go test ./...`.

Validation:

- `go test ./...` and `go test -race ./...` pass locally.

## Additional Changes (2026-02-08)

- **Config-driven tenant selection (boundary-only)**:
  - Later microservice work made `default_tenant_id` explicit runtime config; `ebs_fields.NoebsConfig.Defaults()` must not invent it.
  - Removed the remaining runtime fallback to `store.DefaultTenantID` in `dashboard/db_helpers.go`.
- **Handler slimming / separation work (ongoing)**:
  - Merchant EBS proxy handlers were refactored to a generic helper (`merchant/ebs_proxy.go`) and `merchant/payment_apis.go` was reduced to mostly one-liners.
  - Consumer EBS proxy handlers were refactored to use `consumer/proxyEBS` + `consumer/callEBSJSON` and `consumer/payment_apis.go` dropped from ~2400 LOC to ~1400 LOC.
- **Reduced sensitive logging**:
  - Removed/rewrote handler logs that printed full EBS responses via `%+v` (can include PAN/IPIN-related fields); replaced with minimal status logging.
- **Correctness fixes in handlers**:
  - Fixed several handlers that returned an error response but continued executing (missing `return` after bind/marshal/encrypt failures).
  - Added IPIN base URL entries to `consumer.ToDatabasename` so IPIN transactions record a stable `Name`.
- **Consumer handler architecture aligned closer to wallet**:
  - Introduced `consumer/handler/routes.go` and moved the route list out of `cli/config.go`.
  - Reduced `consumer/handler/ebs.go` from ~585 LOC to ~190 LOC using a shared `handleEBS` helper (`consumer/handler/ebs_helpers.go`).
  - Converted stringly-typed auth errors to sentinel errors in the domain (`consumer/errors.go`) and mapped using `errors.Is` in the handler.
  - Updated consumer tests to be service-level (no Fiber dependency in `consumer` package tests), avoiding import cycles and keeping layer boundaries clean.
- **Merchant handler split (wallet-style)**:
  - Introduced `merchant/handler/*` (Fiber boundary) and removed Fiber from the `merchant` domain package.
  - Moved route wiring to `merchant/handler/routes.go` and `cli/config.go` now registers merchant routes via `merchanthandler.RegisterRoutes`.
  - Deleted legacy handler files in `merchant` (`merchant/payment_apis.go`, `merchant/fiber_helpers.go`) to prevent service/handler mixing.
- **EBS-facing services no longer return HTTP codes**:
  - Added `ebs_fields.CallError` to carry transport status + parsed EBS response on failure.
  - Consumer and merchant EBS operations now return `(ebs_fields.EBSParserFields, error)`; handlers map `CallError.Status` to HTTP responses.
- **Removed handler method multiplexing**:
  - Split consumer beneficiaries endpoint into explicit handlers: `CreateBeneficiary`, `ListBeneficiaries`, `DeleteBeneficiary` and registered per-method routes (no `router.All` + `c.Method()` switch).

## Follow-Ups / Backlog

- Audit logging for accidental leakage of PAN/IPIN (prefer redacted `String()` and avoid `%+v` on structs with sensitive fields).
- Add `golangci-lint` to CI and run a baseline ruleset.
- Improve docs around configuration, security posture, and safe defaults (README + `docs/`).
