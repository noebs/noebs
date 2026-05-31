# Progress

## 2026-05-31

- Created `slop.md` as the candidate register.
- Fixed PSP validation so missing currency now fails explicitly instead of silently allowing provider checks to pass.
- Replaced a custom ASCII-only currency comparison with `strings.EqualFold` plus trimming.
- Fixed wallet validation tenant checks so reserved tenant IDs fail at the validation boundary.
- Fixed the `go vet` protobuf copylock in `EnsureWalletPublic`.
- Made Docker/testcontainers-dependent tests skip only when the container runtime is unavailable, so ordinary `go test ./...` remains useful on machines without Docker.
- Fixed login metrics so first attempts are counted, windows reset after 15 minutes, suspicious increments create missing metric rows, and auth OTP flows record attempts/suspicious failures.
- Removed duplicated non-test EBS fixture helpers from production packages and replaced the live CLI fixture with deterministic `_test.go` helpers.
- Fixed dashboard merchant transaction stats to return query failures as HTTP 500 instead of empty HTTP 200 results.
- Removed the unused `SendPush` no-op placeholder; notification work now stays on persisted notification and notification-chat command paths.
- Fixed utility helper bugs: SMS responses are closed, and `MaskPAN` no longer panics on short inputs.
- Fixed OTP SMS delivery so gateway failures are returned synchronously, non-2xx SMS responses become typed delivery errors, and the HTTP handler reports delivery failures as HTTP 502 instead of `not_found`.
- Fixed shared validator initialization so setup errors are returned through `ValidateStruct` instead of terminating the process from `ebs_fields`.
- Stopped user create/update paths from persisting main-card expiry and constrained generic user-column updates to a known safe set.
- Fixed dashboard transaction decoding so malformed stored payloads fail loudly and fetch/decode errors return HTTP 500 instead of being swallowed or treated as missing rows.
- Fixed sensitive-field encryption helpers so crypto failures are returned and failed encryption does not partially mutate user/card/cache-card structs.
- Fixed sensitive-field hydration so corrupt ciphertext and failed legacy plaintext backfills surface as store read errors.
- Fixed store transaction and notification readers so malformed persisted JSON returns contextual decode errors instead of zero-value payloads.
- Fixed OTP verification so verified-user flag updates are required to succeed and the returned user reflects the persisted verified state.
- Fixed bill due-amount parsing so malformed gateway payment-info maps return typed validation errors instead of panics or empty amounts.
- Fixed merchant NEC bill parsing so malformed receipt maps return typed errors instead of panics or silently zeroed amounts.
- Fixed dashboard browser/export handlers so stats query, JSON encode, and stream errors return HTTP 500 instead of being ignored.
- Added a top-level `parsing` package for shared map-field parsing and moved bill/receipt extraction onto it; removed the unused `utils.GetOrDefault` helper.
- Moved consumer/merchant handler dependency validation to construction so nil services or missing stores fail during HTTP startup.
- Removed redundant consumer/merchant per-request handler nil checks now covered by startup construction invariants.
- Fixed HTTP JSON PSP response handling so body read failures and malformed 2xx JSON do not map to empty successful responses.
- Fixed Google OAuth user creation/linking so provider lookup errors are not treated as misses and new users plus auth accounts are persisted atomically.
- Fixed wallet workflow activity-name scheduling and compensation handling so hold release, usage, ownership, and manual-transfer status failures are not hidden.

Verification:

- `go test ./wallet/validation`
- `go test -count=1 ./store ./consumer`
- `go test -count=1 . ./cli`
- `go test -count=1 ./dashboard`
- `go test -count=1 ./utils`
- `go test -count=1 ./consumer ./consumer/handler`
- `go test -count=1 ./ebs_fields`
- `go test -count=1 ./store`
- `go test -count=1 ./dashboard`
- `go test -count=1 ./parsing ./utils ./consumer ./merchant`
- `go test -count=1 ./consumer/handler ./merchant/handler ./cli`
- `go test -count=1 ./wallet/psp ./wallet/psp/httpjson`
- `go test -count=1 ./store ./consumer`
- `go test -count=1 ./wallet/activity ./wallet/workflow`
- `go test -count=1 ./...`
- `go vet ./...`

Next candidates:

- Continue scanning for remaining TODO/FIXME markers, silent defaults, hidden errors, dead paths, and tooling gaps.
