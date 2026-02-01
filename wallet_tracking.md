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

## Next steps (short-term)
- [ ] Implement workflow logic + PSP provider implementations

## Notes / decisions
- Migrations will be split into small, focused files to keep changes atomic.
- Wallet IDs will use UUIDs; migration includes pgcrypto extension for `gen_random_uuid()`.
