# Slop Candidate Register

Last updated: 2026-05-31

## Fixed

1. PSP config validation accepted an empty currency.
   - Evidence: `wallet/validation/service.go` returned nil when `ValidatePSPConfig` received an empty currency.
   - Fix: trim and require currency with `walletstore.ErrMissingCurrency`; replace the hand-rolled case-folding helper with `strings.EqualFold`.
   - Tests: `TestValidatePSPConfigRequiresExplicitCurrency`, `TestValidatePSPConfigMatchesTrimmedCurrency`.

2. Wallet validation helpers only checked for missing tenant IDs.
   - Evidence: `ValidateP2PRequest`, `ValidateDepositRequest`, `ValidateWithdrawalRequest`, and PSP amount resolution did not reject the reserved `default` tenant at the validation boundary.
   - Fix: route tenant checks through `walletstore.ValidateTenantID`.
   - Tests: reserved-tenant cases in wallet validation tests plus `TestResolvePSPDepositAmountsRejectsReservedTenant`.

3. `go vet ./...` failed on a protobuf copylock.
   - Evidence: `wallet/grpc/server.go` copied `EnsureWalletRequest` by value in `EnsureWalletPublic`.
   - Fix: build a fresh bounded request with explicit fields instead of copying the protobuf message state.

4. Integration tests failed hard when Docker/testcontainers was unavailable.
   - Evidence: `go test ./...` failed with container provider errors before running non-container tests.
   - Fix: centralize container-runtime-unavailable detection in `internal/testdb` and skip only those integration tests when the runtime is absent. Real container/database startup errors still fail.
   - Tests: `internal/testdb` classifier tests; `go test ./...` now passes in this environment.

5. Login metrics had broken window semantics and were disconnected from auth flows.
   - Evidence: `RecordLoginAttempt` reset `window_started_at` on every increment, returned `0` for the first increment, and `IncrementSuspicious` silently did nothing when no row existed. `GenerateSignInCode` and invalid OTP verification did not touch the metric path.
   - Fix: make login metrics atomic upserts with a 15-minute window, validate mobile before DB access, count first attempts correctly, create missing suspicious rows, increment sign-in code attempts, and increment suspicious counts on invalid OTP.
   - Tests: store login metric tests and consumer auth metric tests.

6. EBS test fixtures were duplicated, non-deterministic, and compiled into normal packages.
   - Evidence: root `test_helpers.go` and `cli/test_helpers.go` were identical non-test files with FIXME comments, `math/rand`, wall-clock timestamps, and `testing` imports.
   - Fix: delete the unused root copy, move the live CLI fixture into `cli/ebs_test_helpers_test.go`, use deterministic fixture values, and simplify payload helpers to require `*testing.T` instead of a fake service parameter.
   - Tests: `go test -count=1 . ./cli`.

7. Dashboard merchant transaction stats hid query failures as empty successful responses.
   - Evidence: `MerchantTransactionsEndpoint` returned HTTP 200 with an empty `MerchantTransactions` result whenever the stats query failed.
   - Fix: return HTTP 500 with the query error message instead of pretending the merchant has empty stats.
   - Tests: `TestMerchantTransactionsEndpointReturnsQueryErrors`.

8. Push delivery exposed an unused no-op service method.
   - Evidence: `consumer/auth_service.go` `SendPush` logs and returns `apperr.ErrUnavailable`.
   - Fix: remove the dead method and its `apperr` dependency; notification delivery now goes through persisted notification records and notification-chat commands.
   - Tests: `go test -count=1 ./consumer`.

9. Utility helpers leaked SMS response bodies and panicked on short PANs.
   - Evidence: `utils.SendSMS` did not close `http.Get` response bodies, and `utils.MaskPAN` sliced without checking length.
   - Fix: close SMS response bodies and make `MaskPAN` return short values unchanged, matching the safer EBS response masker.
   - Tests: `go test -count=1 ./utils`.

10. OTP SMS delivery failures were silently reported as success or as the wrong error.
    - Evidence: `GenerateSignInCode` sent SMS in a goroutine and ignored errors, while `SendSMS` returned nil for non-2xx gateway responses. The HTTP handler also mapped every generation error to `not_found`.
    - Fix: make SMS delivery synchronous for OTP generation, return a typed SMS delivery error on transport or non-2xx gateway failures, bound the SMS HTTP client with a timeout, and map delivery failures to HTTP 502.
    - Tests: `go test -count=1 ./utils`, `go test -count=1 ./consumer ./consumer/handler`.

11. Validator initialization could terminate the process from a shared package.
    - Evidence: `ebs_fields/custom_validator.go` called `log.Fatalf` if custom validation registration failed, using a package logger from another file and exiting instead of returning an error.
    - Fix: store a typed validator initialization error and return it through `ValidateStruct`; keep validator setup lazy without process termination.
    - Tests: `go test -count=1 ./ebs_fields`.

12. User writes persisted main-card expiry and allowed arbitrary user columns.
    - Evidence: `CreateUser` and `UpdateUser` wrote `User.ExpDate` into `users.main_expdate`, despite card expiry belonging to card-vault data. `UpdateUserColumns` built SQL directly from caller-provided map keys.
    - Fix: stop persisting main-card expiry on user create/update, and reject unknown or unsafe user update columns before building SQL.
    - Tests: `go test -count=1 ./store`, `go test -count=1 ./consumer ./consumer/handler`.

13. Dashboard transaction decoding swallowed malformed stored payloads.
    - Evidence: `decodeTransactionRows` ignored `json.Unmarshal` errors, producing zero-value transactions from corrupt payload rows. Some dashboard handlers also mapped fetch/decode failures to 404.
    - Fix: return decode errors with row context, propagate them through transaction fetch/sort helpers, and return 500 for dashboard fetch/decode failures.
    - Tests: `go test -count=1 ./dashboard`.

14. Sensitive-field encryption helpers swallowed encryption failures.
    - Evidence: user, card, cache-card, and generic user-column encryption paths ignored `Encrypt` errors, which could continue writes with plaintext or partial field mutation after a crypto failure.
    - Fix: make sensitive-field encryption helpers return errors, avoid partial mutation on failure, and propagate main-card encryption failures through create/update paths.
    - Tests: `go test -count=1 ./store`.

## Open Candidates

No open candidates in this file yet after the current pass. Continue scanning the repo for remaining TODO/FIXME markers, silent defaults, hidden errors, dead paths, and tooling gaps.
