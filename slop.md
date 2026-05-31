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

15. Sensitive-field hydration hid corrupt ciphertext and failed legacy backfills.
    - Evidence: read-side hydration ignored decrypt errors and dropped errors from best-effort backfill updates for legacy plaintext PAN/IPIN/token fields.
    - Fix: make hydration helpers return decrypt, encryption, and backfill update errors; propagate those errors from user, card, cache-card, and token read methods.
    - Tests: `go test -count=1 ./store`.

16. Store transaction and notification readers swallowed malformed JSON payloads.
    - Evidence: `GetTransactionsByMaskedPan`, `GetTransactionByUUID`, and notification payment-request hydration ignored `json.Unmarshal` errors and returned zero-value payloads.
    - Fix: decode persisted JSON through helpers that return context-rich errors and propagate failures from store readers.
    - Tests: `go test -count=1 ./store`.

17. OTP verification ignored the verified-user persistence update.
    - Evidence: `VerifyOTP` discarded the `UpdateUserColumns` error and returned the stale user struct, so a failed write could still look like a successful verification.
    - Fix: propagate the update error and update the returned user flags after the write succeeds.
    - Tests: `go test -count=1 ./consumer`.

18. Bill due-amount parsing used unchecked gateway payload assertions.
    - Evidence: `parseDueAmounts` used direct `. (string)` assertions for some billers and returned empty amounts for missing required payment-info fields.
    - Fix: validate required payment-info fields with typed `ErrInvalidPaymentInfo` errors instead of panicking or silently returning empty amounts.
    - Tests: `go test -count=1 ./consumer`.

19. Merchant NEC bill parsing used unchecked map assertions and ignored parse errors.
    - Evidence: `necBill.NewFromMap` directly asserted map values to strings and ignored `ParseFloat` errors, allowing panics or zero-value receipts for malformed gateway data.
    - Fix: parse all required fields into locals first, accept numeric amount values explicitly, and return typed `ErrInvalidBillInfo` errors without partial mutation.
    - Tests: `go test -count=1 ./merchant`.

20. Dashboard browser/export paths ignored runtime query and stream errors.
    - Evidence: `BrowserDashboard` logged count/sum query failures and discarded merchant stats query errors; `Stream` discarded JSON encode and `SendStream` errors.
    - Fix: return HTTP 500 on those failures and add a source guard against reintroducing the ignored-error patterns.
    - Tests: `go test -count=1 ./dashboard`.

21. Parsing helpers were scattered across service packages and `utils` carried an unsafe map lookup.
    - Evidence: bill/receipt parsing duplicated string and float extraction logic, and `utils.GetOrDefault` directly asserted `value.(string)` for a `map[string]interface{}` helper.
    - Fix: add a top-level `parsing` package with typed missing/invalid field errors, required string/float helpers, and safe optional string defaults; remove the unused `utils.GetOrDefault` surface and route bill/receipt parsing through the shared package.
    - Tests: `go test -count=1 ./parsing ./utils ./consumer ./merchant`.

22. HTTP handler constructors accepted invalid service wiring, forcing every request path to defensively check impossible nil dependencies.
    - Evidence: `consumer/handler.New` and `merchant/handler.New` returned handlers around nil services or services with nil stores, while `GetMainEngine` assumed construction could not fail.
    - Fix: make handler constructors validate required service/store dependencies and return typed startup errors; route registration now fails during engine construction instead of relying on per-handler compensation.
    - Tests: `go test -count=1 ./consumer/handler ./merchant/handler ./cli`.

23. Handler request paths still carried service nil checks after construction guaranteed service wiring.
    - Evidence: consumer and merchant HTTP handlers repeatedly returned `service_unavailable` for `h == nil || h.Service == nil`, even though routes are registered from validated constructors and nil handlers cannot be produced by normal startup.
    - Fix: remove the redundant per-request nil guards and simplify EBS helper signatures so handlers rely on the startup invariant.
    - Tests: `go test -count=1 ./consumer/handler ./merchant/handler ./cli`.

24. HTTP JSON PSP provider treated malformed successful responses as empty success.
    - Evidence: `wallet/psp/httpjson.Provider.doJSON` ignored `io.ReadAll` and `json.Unmarshal` errors, so read failures or invalid 2xx JSON could continue with an empty mapped PSP response.
    - Fix: return read failures as temporary PSP errors and malformed response JSON as a typed `ErrPSPResponseInvalid`.
    - Tests: `go test -count=1 ./wallet/psp ./wallet/psp/httpjson`.

25. Google OAuth could authenticate users without durably linking the provider account.
    - Evidence: `findOrCreateUserFromGoogle` treated auth-account lookup failures like misses, ignored `LinkAuthAccount` failures, and created a user before linking the Google account in a separate write.
    - Fix: distinguish not-found from real lookup errors, require link success for existing email matches, add typed auth-account validation, and create new Google users plus auth accounts in one store transaction.
    - Tests: `go test -count=1 ./store ./consumer`.

26. Wallet workflows could hide failed compensation and, in tests, could not schedule generated activity names as Temporal activity types.
    - Evidence: withdrawal/manual-transfer paths discarded release-hold, destination usage, funding-source usage, ownership status, and manual-transfer status activity errors. A Temporal workflow regression test also exposed that generated `ActivityName` was a distinct string type, causing `ActivityType is not set` when used with `ExecuteActivity`.
    - Fix: make generated activity names a string alias, propagate compensation/update failures with joined workflow errors, and require withdrawal hold-release failure to surface when approval rejection cleanup fails.
    - Tests: `go test -count=1 ./wallet/activity ./wallet/workflow`.

27. Withdrawal and manual-debit workflows spent reserved funds through the normal double-entry path.
    - Evidence: `CreateHold` already subtracts wallet available balance, but successful withdrawal/manual-debit posting then called `PostDoubleEntry`, which requires available balance again and subtracts it again. The workflow then released the hold after ledger posting, which could restore captured funds to available balance.
    - Fix: add a held double-entry store/activity contract that consumes active hold balance without a second available-balance debit, mark fully consumed holds as `captured`, and route withdrawal/manual-debit ledger posting through that contract.
    - Tests: `go test -count=1 ./wallet/store ./wallet/activity ./wallet/workflow`; `go test -count=1 -v ./wallet/store -run 'TestLedgerAccounting|TestPostHeldDoubleEntryValidation'` (Postgres container case skipped locally because the container runtime is unavailable).

28. Deposit and manual-credit workflows required treasury pre-funding before external credits could post.
    - Evidence: deposits and manual credits debit a system treasury wallet with `PostDoubleEntry`. `EnsureSystemWallet` creates treasury at zero balance, so the first real deposit/manual credit can fail with `ErrInsufficientFunds` before crediting the target wallet.
    - Fix: add an explicit system-debit double-entry store/activity contract that only permits system debit wallets to overdraft, and route deposit/manual-credit ledger posting through it while leaving ordinary transfers on the available-balance path.
    - Tests: `go test -count=1 ./wallet/store ./wallet/activity ./wallet/workflow`; `go test -count=1 -v ./wallet/store -run 'TestLedgerAccounting|TestPostHeldDoubleEntryValidation'` (Postgres container case skipped locally because the container runtime is unavailable).

29. Manual transfers accepted arbitrary transfer types and unknown types bypassed hold semantics.
    - Evidence: request/admin/workflow/store validation only rejected an empty `transfer_type`. In the workflow, only `manual_debit` and `manual_withdrawal` created holds, while unknown non-empty types could fall through to the ordinary ledger path.
    - Fix: add typed manual-transfer type validation for `manual_credit`, `manual_debit`, and `manual_withdrawal`; enforce it at gRPC/admin boundaries, workflow startup, and store create.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc ./wallet/workflow`.

30. Ledger idempotency accepted mismatched duplicate requests as successful replays.
    - Evidence: when `PostDoubleEntry` hit an existing `(tenant_id, idempotency_key)`, it returned the stored ledger entries without verifying currency, reference, debit wallet, credit wallet, or amount matched the new request.
    - Fix: validate existing ledger transaction and debit/credit entries against the requested double-entry contract; exact replays return `Existing`, mismatched replays return `ErrDuplicateTransaction`.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestLedgerAccounting|TestExistingDoubleEntryMatches|TestPostHeldDoubleEntryValidation'` (Postgres container case skipped locally because the container runtime is unavailable).

31. Balance hold creation could fail exact replays and accept mismatched duplicate holds.
    - Evidence: `CreateHold` checked current available balance before detecting an existing hold, so exact retries after funds were reserved could return `ErrInsufficientFunds`. When a reference conflict was found, it returned the existing hold without verifying amount, reason, idempotency key, or active/unconsumed status.
    - Fix: handle existing hold conflicts before the new-hold funds check; exact active/unconsumed replays return the original hold, mismatched conflicts return `ErrDuplicateHold`.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestLedgerAccounting|TestExistingHoldMatches|TestExistingDoubleEntryMatches|TestPostHeldDoubleEntryValidation'` (Postgres container case skipped locally because the container runtime is unavailable).

32. Deposit/withdrawal request replay reused existing PSP workflows without validating the replay contract.
    - Evidence: `RequestDeposit` and `RequestWithdrawal` returned an existing workflow for any matching `client_reference`, even when provider, idempotency key, direction, amount, fee, net amount, or currency differed. `CreatePSPTransaction` also returned raw duplicate-key errors instead of supporting exact idempotent replays.
    - Fix: add PSP transaction create-replay validation, use it in the store and gRPC request boundaries, and return `ErrDuplicateTransaction` for mismatched duplicates.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestValidatePSPTransactionCreateReplay|TestUpdatePSPTransactionStatus'` (Postgres container case skipped locally because the container runtime is unavailable).

33. Deposit/withdrawal workflow start failures could strand PSP transactions in `initiated`.
    - Evidence: after creating the PSP transaction, `RequestDeposit` and `RequestWithdrawal` discarded `UpdatePSPTransactionStatus` errors when Temporal workflow start failed, hiding the local state repair failure from callers and leaving pollable initiated rows behind.
    - Fix: centralize PSP workflow-start failure recording, mark the row `failed`, return joined errors when the repair write fails, and map that joined error as an internal gRPC failure instead of hiding it.
    - Tests: `go test -count=1 ./wallet/grpc`.

34. Funding source totals were updated before the idempotent ledger link existed.
    - Evidence: deposit workflow passed the deposit amount into `UpsertFundingSource`, which incremented `funding_sources.total_funded` independently from `ledger_funding_links`. A retry after the source write but before/during link recording could overcount return-to-source capacity, duplicate source identities with different provider/currency were silently merged, and the nullable `external_reference` uniqueness did not protect sources without an external reference.
    - Fix: make funding-source upsert validate immutable source identity and metadata only, move `total_funded` increments into `CreateFundingLink`, make ledger funding links exact-replay idempotent with mismatches returning typed duplicate errors, and add a partial unique index for null external references.
    - Tests: `go test -count=1 ./wallet/store ./wallet/workflow ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestFundingSourceTotalsFollowIdempotentLedgerLinks|TestValidateFundingSourceMerge|TestValidateFundingLinkReplay|TestFundingSourceValidation|TestCreateFundingLinkValidation'` (Postgres container case skipped locally when the container runtime is unavailable).

35. Return-to-source withdrawal usage could overcount funding source capacity on retry.
    - Evidence: after withdrawal ledger posting, the workflow called `UpdateFundingSourceUsage`, which incremented `funding_sources.total_withdrawn` without a ledger-entry idempotency key. A retry after the DB update could consume return-to-source capacity multiple times for one ledger debit.
    - Fix: use `ledger_funding_links` for debit entries too, validate each link against the ledger entry amount/currency/type, and update `total_funded` for credit links or `total_withdrawn` for debit links only when a new link is inserted.
    - Tests: `go test -count=1 ./wallet/store ./wallet/workflow ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestFundingSourceTotalsFollowIdempotentLedgerLinks|TestValidateFundingLinkLedgerEntry|TestValidateFundingLinkReplay'` (Postgres container case skipped locally when the container runtime is unavailable).

36. Withdrawal destination usage could overcount destination totals on workflow retry.
    - Evidence: after withdrawal ledger posting, the workflow called `UpdateWithdrawalDestinationUsage`, which incremented `withdrawal_destinations.total_withdrawn` without a ledger-entry idempotency key. The same retry shape as funding-source usage could double count one withdrawal, and the old unkeyed usage activity/store APIs were still registered.
    - Fix: add `ledger_withdrawal_destination_links`, route destination usage through exact-replay ledger links, validate the link against the debit ledger entry, destination wallet/currency, and funding-source wallet/currency, and remove the unkeyed funding/destination usage activities.
    - Tests: `go test -count=1 ./wallet/store ./wallet/activity ./wallet/workflow ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestFundingSourceTotalsFollowIdempotentLedgerLinks|TestCreateWithdrawalDestinationLinkValidation|TestValidateWithdrawalDestinationLinkLedgerEntry|TestValidateWithdrawalDestinationLinkReplay'` (Postgres container case skipped locally when the container runtime is unavailable).

## Open Candidates

No open candidates in this file yet after the current pass. Continue scanning the repo for remaining TODO/FIXME markers, silent defaults, hidden errors, dead paths, and tooling gaps.
