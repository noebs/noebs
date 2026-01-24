# Modernization Tracking

Date started: 2026-01-24

## Goals
- Keep API behavior stable while modernizing internals.
- Replace GORM with a simpler, explicit SQL layer.
- Remove Redis usage (move data to SQL).
- Add strong unit-test invariants (tests first, then migrations).
- Introduce proper context propagation (no hidden context.Background in helpers).
- Move primary DB to Postgres via Docker Compose, keep SQLite for local/dev or tests.
- Support Google sign-in (optional) while preserving existing mobile auth.
- Make the system multi-tenant by design (tenant scoping everywhere).

## Decisions (pending)
- SQL layer: sqlx vs sqlc vs database/sql + hand-written queries.
- Multi-tenant strategy: shared schema with tenant_id vs per-tenant schema.
- Migration tooling: goose, migrate, atlas, or plain SQL.

## Invariants (tests to preserve)
- Auth (mobile): register/login flows return expected status codes and JWT headers.
- Notifications: authenticated request returns user notifications.
- Cards: add/edit/remove/get cards work and return the same response shapes.
- Redis-backed lookups (legacy): card-from-mobile behavior remains identical until replaced.

## Migration Plan
1) Tests-first: expand unit tests to capture current API behavior.
2) Introduce storage interfaces (repositories) to isolate DB/cache logic.
3) Replace Redis usage with SQL tables + queries (keep behavior stable).
4) Replace GORM models with explicit SQL (sqlx/sqlc) and migrations.
5) Multi-tenant support: add tenant_id to auth, DB schema, and queries.
6) Auth: keep mobile login; add/keep Google auth linking to users.
7) Docker Compose: add Postgres service and env wiring.
8) Cleanup: remove Redis + GORM dependencies and update docs.

## Progress Log
- 2026-01-24: Created tracking doc; started adding tests and redis test harness.
- 2026-01-24: Added manual SQL store + migrations, tenant-aware queries, postgres-compose setup, and removed Redis/GORM from runtime paths (tests updated or tagged legacy where needed).
- 2026-01-24: Switched runtime config loading to merge config.yaml + secrets.yaml (SOPS), removed env overrides and .secrets.json dependency, and added docker-specific config for Postgres compose.
- 2026-01-24: Replaced custom migrator with goose (embedded SQL + Go migrations) and added a backfill migration to ensure tenant_id columns and legacy push_data/transaction fields are migrated.
- 2026-01-24: Removed GORM dependencies entirely (legacy GORM tests deleted; manual SQL is the only persistence path).
- 2026-01-24: Introduced typed app errors and standardized JSON error responses in fiber helpers.
- 2026-01-24: Removed Firebase integration and dependencies; push delivery is now a no-op and device tokens are stored without Firebase SDK.
