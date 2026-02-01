# Wallet Handoff Notes (For Other Agents)

Last updated: 2026-02-01

This doc is for agents who do not share the current context. It summarizes missing workstreams, key files, and the intended contracts.

## High-level status
- Workflows are implemented and started via gRPC endpoints.
- PSP abstraction exists (loader, registry, activities), but only noop provider is registered.
- Funding sources and destinations are stored and enforced in workflows, but no APIs exist to manage them.
- Admin BO routes exist only as stubs; no HTMX/approval UX yet.

## Core workflow entrypoints (already implemented)
- Deposit workflow start: `wallet/grpc/deposit.go`
- Withdrawal workflow start + approval/verification signals: `wallet/grpc/withdrawal.go`
- P2P workflow start: `wallet/grpc/p2p.go`
- Manual transfer workflow start + decision signal: `wallet/grpc/manual_transfer.go`
- Workflow logic: `wallet/workflow/workflows.go`

## PSP integrations (missing)
### What to build
- Real provider adapters in `wallet/psp/<provider>` implementing `wallet/psp/provider.go`.
- Provider registration in `cli/config.go` (see noop registration block).
- Webhook routing endpoint (HTTP handler):
  - Verify signature using `wallet/psp/loader.go` + provider `VerifyWebhook`.
  - Map payload to a `client_reference` and update `psp_transactions`.
  - Signal workflows by `client_reference` (Temporal signal or start if missing).
- Error mapping: use typed errors in `wallet/psp/errors.go`.

### Useful files
- `wallet/psp/loader.go` (DB + SOPS secret merge)
- `wallet/psp/registry.go` (provider registry)
- `wallet/activity/psp.go` (activity that calls provider)
- `wallet/store/psp_transactions.go` (psp transaction persistence)

## PSP status polling schedule (missing)
- Workflow exists: `wallet/workflow/workflows.go` -> `PSPStatusPoller`.
- No schedule/cron exists to kick it off.
- Create schedules in CLI bootstrap (per tenant) or via Temporal cron.

## Funding source + withdrawal destination APIs (missing)
### Store helpers already exist
- Funding source: `wallet/store/funding.go`
- Destinations: `wallet/store/destinations.go`
- Ownership verification: `wallet/store/ownership.go`

### Endpoints to add (gRPC + gateway; optional Fiber handlers)
- Create/List funding sources (likely read-only for users).
- Create/List withdrawal destinations.
- Trigger ownership verification (create record) and submit verification result.
- Deactivate destination.

### Workflow expectations
- Withdrawal workflow already enforces verification (`wallet/workflow/workflows.go`).
- Ownership verification is completed via workflow signal `WithdrawalVerificationSignal`.

## Admin BO (HTMX/HATEOAS) (missing)
### Existing stubs
- `wallet/handler/admin.go` and `wallet/handler/routes.go`

### What to implement
- Approvals queue (manual transfers + withdrawals).
- Approve/Reject actions (signal workflows).
- Audit log viewer.
- Wallet list/details views.

## Security UX (missing)
- Wallet PIN set/reset flows.
- User 2FA enrollment/disable flows (TOTP verification exists in `wallet/activity/security.go`).

## Testing gaps (missing)
- Workflow determinism tests for deposit/withdrawal/manual flows.
- PSP mock server and webhook/poller race tests.
- End-to-end flow: deposit -> P2P -> withdrawal with destination verification.

## Config defaults to know
- Wallet timeout defaults are applied server-side in gRPC handlers (not by clients):
  - `wallet_hold_expiry_seconds`
  - `wallet_approval_timeout_seconds`
  - `wallet_verification_timeout_seconds`
  - `wallet_manual_approval_timeout_seconds`
- Defined in `ebs_fields/fields.go` and sample in `config.yaml`.

## Conventions
- Do not default identifiers in store/service layers (see `AGENTS.md`).
- All external S2S traffic should be gRPC; public APIs are JSON + OpenAPI (gateway).
- Mocks should be generated via GoMock.
