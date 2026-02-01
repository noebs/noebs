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

## Next steps (short-term)
- [ ] Add wallet handlers + route registration (user/admin skeletons)

## Notes / decisions
- Migrations will be split into small, focused files to keep changes atomic.
- Wallet IDs will use UUIDs; migration includes pgcrypto extension for `gen_random_uuid()`.
