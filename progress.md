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

Verification:

- `go test ./wallet/validation`
- `go test -count=1 ./store ./consumer`
- `go test -count=1 . ./cli`
- `go test -count=1 ./dashboard`
- `go test -count=1 ./utils`
- `go test -count=1 ./consumer ./consumer/handler`
- `go test -count=1 ./...`
- `go vet ./...`

Next candidates:

- Continue scanning for remaining TODO/FIXME markers, silent defaults, hidden errors, dead paths, and tooling gaps.
