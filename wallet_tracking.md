# Wallet Implementation Tracking

Last updated: 2026-02-01

## Current focus
- [ ] Phase 1: Foundation (wallet core tables + ledger invariants)

## Completed
- [x] Read unlocked.md and staged initial implementation plan for Phase 1
- [x] Added core wallet tables migration (wallets, ledger_transactions, ledger_entries, balance_holds)
- [x] Added money type scaffold (`wallet/money/money.go`) and decimal dependency
- [x] Added fee/rate/limit tables migration
- [x] Added PSP config + PSP transaction tables migration
- [x] Added admin/manual transfer/audit tables migration
- [x] Added funding/withdrawal/ownership tables migration (AML)
- [x] Added wallet/Temporal config fields + defaults
- [x] Added SOPS secrets skeleton for Temporal + PSP
- [x] Added wallet store scaffold + system wallet ensure helpers
- [x] Added ledger posting + holds helpers with validation tests (no defaults)
- [x] Added wallet service wiring over store layer
- [x] Added wallet user/admin handler skeletons + route registration
- [x] Added PSP provider interface + registry skeleton
- [x] Added PSP config store query helper
- [x] Added PSP config merge + loader skeleton
- [x] Added Temporal worker scaffold + task queue constants
- [x] Wired Temporal worker startup (config-gated)
- [x] Added ledger activities wrapper (double-entry + holds)
- [x] Added workflow stubs + worker registration hook
- [x] Added PSP activities + worker wiring hooks
- [x] Added PSP map-based secret resolver
- [x] Added mock PSP provider scaffold
- [x] Added noop PSP provider scaffold
- [x] Implemented initial P2P workflow using ledger activity
- [x] Implemented deposit workflow stub calling PSP verify activity
- [x] Implemented withdrawal workflow stub calling PSP payout activity
- [x] Added fee/rate/limit store query helpers
- [x] Added fee engine, rate service, and limit enforcer helpers
- [x] Added wallet PIN + TOTP helpers
- [x] Added RBAC permission constants + role helper
- [x] Added admin role/user store helpers
- [x] Added audit log store helper
- [x] Added manual transfer store helpers
- [x] Added funding source store helpers
- [x] Added withdrawal destination + ownership store helpers
- [x] Added wallet list store helper + admin list handler
- [x] Refactored wallet store queries to use ensured DB handle
- [x] Added Temporal services to docker-compose
- [x] Added PSP transaction model + store helpers
- [x] Added funding source activities for Temporal workflows
- [x] Added ownership verification activities for Temporal workflows
- [x] Added wallet PIN verification activity
- [x] Added audit event activity
- [x] Added PSP transaction status activity
- [x] Added manual transfer activities for workflows
- [x] Registered noop PSP provider for Temporal workflows
- [x] Added PSP transaction status update store helper
- [x] Added fee + limit activities for workflows
- [x] Added exchange rate conversion activity
- [x] Added explicit validation activities for ledger operations
- [x] Added PSP poller query helper
- [x] Added validation service + activity for P2P business rules
- [x] Switched PSP mocks to GoMock-generated implementation
- [x] Added deposit/withdrawal validation service + activities
- [x] Generated typed enums for activity names and task queues
- [x] Added global HTTP client with override options for service-to-service calls
- [x] Added gRPC proto + Buf configs for internal S2S and public JSON/OpenAPI generation
- [x] Added proto generation test + Temporal testcontainers smoke test
- [x] Added PSP transaction amount ledger for multi-currency/over-under payment tracking
- [x] Added PSP amount resolution logic (accept under/overpayment by default; credit converted amount)
- [x] Added PSP transaction amount batch insert API + activity
- [x] Wired PSP amount resolution + recording into deposit/withdraw workflows
- [x] Implemented PSP status poller workflow + polling activities
- [x] Updated deposit/withdraw workflows to persist PSP status + provider transaction IDs
- [x] Added deposit/withdraw ledger postings (treasury + fees) on PSP success
- [x] Recorded funding sources and linked deposit ledger credits
- [x] Made PSP amount inserts idempotent via upserts
- [x] Added P2P fee ledger postings to system fees wallet
- [x] Added gRPC wallet service handlers and generated proto stubs/OpenAPI
- [x] Added gRPC gateway helpers for public wallet service
- [x] Wired gRPC server/gateway startup hooks in CLI config
- [x] Added PSP transaction lock acquisition for poller safety
- [x] Mounted gRPC gateway routes on Fiber instead of standalone HTTP server
- [x] Implemented manual transfer workflow with approval/hold/ledger posting
- [x] Added reconciliation workflow to detect missing ledger entries
- [x] Added funding source/destination usage updates and ownership verification status updates
- [x] Added user 2FA verification activity and wired it into wallet worker setup
- [x] Implemented withdrawal flow with PIN/2FA, return-to-source, destination verification, holds, and approvals
- [x] Added audit events across deposit/withdrawal/P2P/manual transfer/reconciliation workflows
- [x] Added withdrawal request + approval/verification gRPC endpoints with Temporal workflow start/signals
- [x] Wired gRPC server Temporal client options + workflow ID persistence in PSP transactions
- [x] Added GoMock Temporal client + withdrawal gRPC tests (validation + workflow start)
- [x] Added P2P workflow gRPC endpoint + validation test
- [x] Added manual transfer gRPC request + decision endpoints with validation tests
- [x] Added deposit gRPC request endpoint + validation test
- [x] Added P2P PIN/2FA enforcement in workflow + gRPC request validation
- [x] Added server-side default timeouts for withdrawal/manual transfer with config defaults

## Next steps (short-term)
- [ ] Implement PSP provider integrations beyond noop (see “Missing workstreams” for context)
- [ ] Add admin BO endpoints (HTMX/HATEOAS) for approvals, audit, and manual transfers
- [ ] Add funding source + destination management APIs (create/list/verify)

## Missing workstreams (handoff-ready, detailed)
- [ ] **PSP providers + webhooks**
  - Implement real provider adapters under `wallet/psp/<provider>` using `wallet/psp/provider.go` interface.
  - Register providers in `cli/config.go` (see noop registration).
  - Webhook endpoints are not implemented: add HTTP route + handler to verify signature via `wallet/psp/loader.go`, update `psp_transactions`, and signal workflows by `client_reference`.
  - PSP errors should map to typed errors in `wallet/psp/errors.go`; activities already call provider via `wallet/activity/psp.go`.
- [ ] **PSP webhook routing + poller schedules**
  - Temporal poller workflow exists in `wallet/workflow/workflows.go` (`PSPStatusPoller`), but no Temporal schedule is created.
  - Add schedule creation (or cron) and wiring in CLI bootstrap to kick off poller per tenant.
- [ ] **Funding sources + destinations management APIs**
  - Stores exist: `wallet/store/funding.go`, `wallet/store/destinations.go`, `wallet/store/ownership.go`.
  - Needed endpoints (gRPC + gateway, plus optional Fiber handlers):
    - Create/List funding sources (read-only for users).
    - Create/List withdrawal destinations; set verification method; deactivate destination.
    - Trigger ownership verification explicitly (create record in `ownership_verifications`).
  - Workflow already enforces verification for withdrawals (`wallet/workflow/workflows.go`), but there’s no API to create/verify destinations.
- [ ] **Admin BO (HTMX/HATEOAS)**
  - HTMX admin routes exist only as stubs in `wallet/handler/admin.go`.
  - Implement: approvals queue, approve/reject actions (manual transfers + withdrawals), audit log viewer, wallets list/details.
  - Use store helpers in `wallet/store/*` and signal workflows via gRPC endpoints.
- [ ] **Security UX + lifecycle**
  - Wallet PIN set/reset flows (store `wallets.wallet_pin_hash`) are not exposed.
  - User 2FA enrollment/disable not exposed; `wallet/activity/security.go` uses base user store for TOTP verification.
- [ ] **Test coverage gaps**
  - No workflow determinism tests for deposit/withdrawal/manual flows.
  - No PSP mock server tests; no webhook/poller race tests.
  - No end-to-end flow test: deposit → P2P → withdrawal with destination verification.

## Context pointers (for other agents)
- Workflow starters: `wallet/grpc/deposit.go`, `wallet/grpc/withdrawal.go`, `wallet/grpc/p2p.go`, `wallet/grpc/manual_transfer.go`.
- Workflow logic: `wallet/workflow/workflows.go`.
- PSP loader + registry: `wallet/psp/loader.go`, `wallet/psp/registry.go`, `wallet/psp/secrets.go`.
- Store APIs for funding/destinations: `wallet/store/funding.go`, `wallet/store/destinations.go`, `wallet/store/ownership.go`.
- Temporal worker setup: `wallet/worker/register.go`, `wallet/worker/worker.go`.
- Config defaults: `ebs_fields/fields.go` (wallet timeout defaults).

## Notes / decisions
- Migrations will be split into small, focused files to keep changes atomic.
- Wallet IDs will use UUIDs; migration includes pgcrypto extension for `gen_random_uuid()`.
- PSP resolution today is tenant+provider only (per `psp_configs` + SOPS secrets at `noebs.psp.<tenant>.<provider>`). Missing row => not registered.
- Follow-up needed: add scoped PSP config overrides for region/currency/direction (e.g., `psp_config_overrides` with scope columns + priority), and update loader to resolve most-specific match then fall back.
- Follow-up needed: add per-scope enablement checks (region/currency/direction) in validation service and PSP dispatch; surface clear errors when config exists but disabled for scope.
