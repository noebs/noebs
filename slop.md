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

37. Withdrawal destination creation allowed invalid return-to-source links and seeded usage totals.
    - Evidence: `CreateWithdrawalDestination` accepted `is_return_to_source` without `linked_funding_source_id`, accepted linked funding sources for another wallet/currency/status, and persisted caller-provided `total_withdrawn`/`last_used_at` even though usage is now derived from ledger links.
    - Fix: require linked funding sources for return-to-source destinations at the gRPC boundary and store layer, validate linked source ownership/currency/withdrawability in the store, and reject pre-seeded destination usage fields.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestWithdrawalDestinationValidation|TestValidateWithdrawalDestinationFundingSource'`.

38. PSP amount replays could silently rewrite money and FX facts.
    - Evidence: `AddPSPTransactionAmount` and `AddPSPTransactionAmounts` used `ON CONFLICT ... DO UPDATE` for `(tenant_id, psp_transaction_id, amount_kind, currency)`, so workflow retries with a changed requested/reported/settlement/fee/net amount or FX payload mutated the existing row instead of rejecting a mismatched idempotency replay.
    - Fix: make PSP amount inserts exact-replay idempotent: conflicts fetch the existing amount row, validate tenant/transaction/kind/amount/currency/FX fields, return the existing row on exact replay, and return `ErrDuplicateAmount` on mismatches.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestValidatePSPTransactionAmountReplay|TestPSPTransactionPersistenceReplaysAndStatusUpdates'` (Postgres container case skipped locally when the container runtime is unavailable).

39. Wallet ensure replay could bind callers to the wrong wallet identity or KYC tier.
    - Evidence: `EnsureWallet` inserted with `ON CONFLICT ... DO NOTHING` and then returned the wallet by owner key without validating that the stored `user_id` and `kyc_tier` matched the request. The separate unique user-wallet index could also surface as a raw DB conflict instead of a typed wallet replay error when the same user/currency appeared under a different owner ID.
    - Fix: validate owner types before hitting the DB, make wallet uniqueness conflicts exact-replay idempotent across both owner and user keys, return existing wallets only when tenant/owner/user/currency/KYC match, and return `ErrDuplicateWallet` for mismatches.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestEnsureWallet|TestValidateEnsureWalletReplay|TestGetWalletByOwnerValidation'` (Postgres container case skipped locally when the container runtime is unavailable).

40. 2FA enrollment could silently disable an enabled TOTP secret.
    - Evidence: `CreateOrResetUserTwoFA` used an upsert that overwrote `wallet_user_2fa.secret`, set `enabled = FALSE`, and cleared enable/disable timestamps for any existing user record. Calling enrollment against an already-enabled user could therefore disable 2FA without verifying the current TOTP code.
    - Fix: allow secret regeneration only while the record is pending/disabled, clear stale timestamps for that reset path, preserve enabled records unchanged, and return `ErrUserTwoFAAlreadyEnabled` when enrollment is attempted against active 2FA.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc ./wallet/handler`; `go test -count=1 -v ./wallet/store -run 'TestCreateOrResetUserTwoFA|TestUserTwoFAValidation'` (Postgres container case skipped locally when the container runtime is unavailable).

41. Manual transfer activity retries were not exact-replay idempotent.
    - Evidence: `CreateManualTransfer` and `AddManualTransferApproval` inserted into tables with unique workflow/idempotency/approver keys but did not handle conflicts, so a retry after a successful DB write could fail the workflow with a raw unique violation. Creation also accepted approved/completed metadata even though manual transfers must start pending.
    - Fix: make manual transfer creation and approval insertion return existing rows only on exact replay, reject mismatched duplicates with typed errors, validate transfer status/approval decisions before the DB, and reject pre-seeded approval/completion fields at creation.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc ./wallet/handler`; `go test -count=1 -v ./wallet/store -run 'TestCreateManualTransfer|TestAddManualTransferApproval|TestValidateManualTransfer|TestListManualTransfersByStatus|TestManualTransferAndApprovalReplaysAreExact'` (Postgres container case skipped locally when the container runtime is unavailable).

42. PSP status updates accepted invalid states and hid terminal-state contradictions.
    - Evidence: `CreatePSPTransaction`, `UpdatePSPTransactionStatus`, and PSP status list filters only checked for empty status strings, relying on DB check constraints or empty query results for invalid values. `UpdatePSPTransactionStatus` also returned nil when a terminal transaction received a different later status, hiding provider/webhook/poller contradictions.
    - Fix: add explicit PSP status validation, apply it before DB work on create/update/list paths, and return `ErrInvalidStatusTransition` when a terminal status would be changed to a different state while preserving exact terminal replays.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc ./wallet/handler`; `go test -count=1 -v ./wallet/store -run 'TestCreatePSPTransactionValidation|TestValidatePSPStatusTransition|TestUpdatePSPTransactionStatusValidation|TestListPSPTransactions|TestPSPTransactionPersistenceReplaysAndStatusUpdates'` (Postgres container case skipped locally when the container runtime is unavailable).

43. Withdrawal ownership state could be bypassed below the workflow boundary.
    - Evidence: destination ownership and ownership-verification status writes accepted any non-empty string at the store API, even though migrations/workflows only understand specific states. `CreateWithdrawalDestinationLink` also attached usage totals to inactive or unverified destinations if called directly through the store/activity layer.
    - Fix: add explicit destination ownership and ownership-verification status validators, enforce them on create/update paths, require destination usage links to target active verified destinations, and map the typed destination-not-verified error at API boundaries.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc ./wallet/handler`; `go test -count=1 -v ./wallet/store -run 'TestUpdateWithdrawalDestinationOwnershipValidation|TestCreateOwnershipVerificationValidation|TestUpdateOwnershipVerificationStatusValidation|TestWithdrawalDestinationValidation|TestValidateWithdrawalDestinationLinkLedgerEntry|TestFundingSourceTotalsFollowIdempotentLedgerLinks'` (Postgres container case skipped locally when the container runtime is unavailable).

44. Ownership verification initiation was not idempotent by workflow or request reference.
    - Evidence: `CreateOwnershipVerification` stored `workflow_id` and `reference_id` but had no unique replay key and no conflict handling. An activity retry after a successful insert could create a second pending verification for the same withdrawal request, leaving callers/signals split across multiple verification IDs.
    - Fix: add partial unique indexes for non-empty `(tenant_id, destination_id, workflow_id)` and `(tenant_id, destination_id, reference_id)`, make creation `ON CONFLICT DO NOTHING`, return existing rows only when the creation contract matches, and return `ErrDuplicateVerification` on mismatched duplicates.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc ./wallet/handler`; `go test -count=1 -v ./wallet/store -run 'TestOwnershipVerificationCreateReplaysAreExact|TestValidateOwnershipVerificationCreateReplay|TestCreateOwnershipVerificationValidation|TestUpdateOwnershipVerificationStatusValidation'` (Postgres container case skipped locally when the container runtime is unavailable).

45. EBS transaction writes silently stored empty payloads when response JSON encoding failed.
    - Evidence: `insertTransaction` and `UpsertTransactionProjection` ignored `json.Marshal` errors for `EBSResponse`. That response includes float fields and free-form mini-statement maps, so NaN/Inf or unsupported map values could produce a marshal error; the code then stored `""` as `transactions.payload`, and read paths decoded that as a zero-value transaction.
    - Fix: centralize transaction payload marshaling, propagate encoding failures with context, and use the helper in both direct transaction inserts and projection upserts.
    - Tests: `go test -count=1 ./store`; `go test -count=1 -v ./store -run 'TestMarshalTransactionPayloadRejectsUnsupportedValues|TestUpsertTransactionProjectionRejectsUnmarshalablePayloadBeforeDB|TestDecodeStoredTransactionPayload|TestStoreUpsertTransactionProjection|TestStoreCreateTransactionWithEvent'` (Postgres container cases skipped locally when the container runtime is unavailable).

46. Targeted root-store updates silently succeeded when the row was missing.
    - Evidence: `UpdateUserColumns`, `MarkTokenPaid`, `UpdateTokenCard`, and `UpdatePaymentRequest` executed keyed updates and returned nil even when `RowsAffected()` was zero. That let OTP verification, quick-pay token completion, token card binding, and payment-request updates report success while mutating nothing.
    - Fix: add a shared rows-affected contract for keyed updates and return `sql.ErrNoRows` when a targeted update touches no rows.
    - Tests: `go test -count=1 ./store`; `go test -count=1 -v ./store -run 'TestExecContextRequireRowsAffected|TestStoreTargetedUpdatesReportMissingRows|TestStoreTenantValidation'` (Postgres container case skipped locally when the container runtime is unavailable).

47. KYC writes could create or split identity records below the service boundary.
    - Evidence: `consumer.Service.UpdateKYC` built KYC/passport rows directly from request mobile data without verifying the user existed, while `store.UpdateKYC` only checked that the KYC pointer was non-nil. Direct or malformed calls could create KYC/passport rows for nonexistent users or write KYC and passport records under different mobiles.
    - Fix: resolve the user at the service boundary, canonicalize KYC/passport identity fields from the persisted user, add typed store validation for missing/mismatched mobiles, and require the target user row before inserting KYC or passport data.
    - Tests: `go test -count=1 ./store ./consumer ./consumer/handler`; `go test -count=1 -v ./store -run 'TestStore_UpdateKYCValidationFailsBeforeDB|TestStore_UpdateKYCRequiresExistingUser|TestStoreTenantValidation'`; `go test -count=1 -v ./consumer -run 'TestUpdateKYCRequiresExistingUser|TestUpdateKYCPersistsForExistingUser|TestUserServiceTenantValidationFailsBeforeDB'` (Postgres container cases skipped locally when the container runtime is unavailable).

48. PSP response amount mapping silently converted malformed provider values into money facts.
    - Evidence: `wallet/psp.MapResponse` parsed configured amount paths with ignored parse errors and direct float truncation. A provider response like `"12.34"`, `json.Number("12.5")`, or `12.5` became `0` or `12` instead of a typed invalid-response error, allowing HTTP PSP providers and webhook signals to carry bad settlement amounts into wallet workflows.
    - Fix: make response amount mapping return `ErrPSPResponseInvalid` for missing or malformed configured amount paths, preserve absent amounts only when no amount mapping is configured, and propagate mapping errors through HTTP PSP provider and webhook mapping paths.
    - Tests: `go test -count=1 ./wallet/psp ./wallet/psp/httpjson ./wallet/handler`; `go test -count=1 -v ./wallet/psp -run 'TestMapResponse'`; `go test -count=1 -v ./wallet/psp/httpjson -run 'TestVerifyDepositRejectsInvalidMappedAmount|TestDoJSONReturnsInvalidResponseError'`; `go test -count=1 -v ./wallet/handler -run 'TestMappedPSPWebhookFields'`.

49. PSP request mapping silently dropped configured outbound fields.
    - Evidence: `wallet/psp.MapRequest` skipped a configured field when its source path was missing and ignored empty target/source mapping paths. A bad PSP mapping or incomplete payout destination could therefore issue an HTTP provider request with required fields omitted instead of failing before the network boundary.
    - Fix: make request mapping return typed errors, reject empty mapping paths as `ErrPSPConfigInvalid`, reject missing configured source paths as `ErrPSPRequestInvalid`, and propagate those errors through HTTP PSP provider methods before outbound calls.
    - Tests: `go test -count=1 ./wallet/psp ./wallet/psp/httpjson`; `go test -count=1 -v ./wallet/psp -run 'TestMapRequest|TestMapResponse'`; `go test -count=1 -v ./wallet/psp/httpjson -run 'TestSendPayoutRejectsMissingMappedSourceBeforeHTTP|TestVerifyDepositRejectsInvalidMappedAmount'`.

50. PSP response string mapping corrupted numeric identifiers with trailing zeroes.
    - Evidence: `wallet/psp.stringFromPaths` formatted `float64` values with `%.0f` and then trimmed trailing `0` characters, so configured numeric identifiers like `1000` or `2500` became `"1"` or `"25"`. That could corrupt numeric provider transaction IDs, client references, or response messages before workflow/status matching.
    - Fix: preserve integral numeric strings with `strconv.FormatInt`, use `strconv.FormatFloat` for non-integral values, and reject NaN/Inf string values as empty.
    - Tests: `go test -count=1 -v ./wallet/psp -run 'TestMapResponsePreservesNumericStringFields|TestMapResponseUsesConfiguredPaths|TestMapResponseRejectsInvalidConfiguredAmount'`.

51. Workflow PSP raw-response fallback corrupted numeric IDs and truncated fractional amounts.
    - Evidence: `statusFromPSPTransaction` reconstructs provider status from stored raw PSP payloads using workflow-local map helpers. Those helpers had the same trailing-zero string bug for numeric transaction IDs and directly cast `float64` amounts to `int64`, so raw fallback payloads could turn `2500` into `"25"` or `12.5` into `12`.
    - Fix: preserve integral numeric strings, format non-integral strings without trimming significant digits, and ignore NaN/Inf/fractional raw amounts instead of truncating them into minor units.
    - Tests: `go test -count=1 -v ./wallet/workflow -run 'TestStatusFromPSPTransaction'`.

52. Balance-hold replay ignored expiry and metadata mismatches.
    - Evidence: `CreateHold` treats duplicate `(tenant_id, wallet_id, reference_type, reference_id)` inserts as idempotent replays, but `existingHoldMatches` only compared amount, reason, reference, idempotency key, and active status. A retry or direct store call with a different `expires_at` or hold metadata returned the existing hold as success, hiding mismatched lock lifetime or audit/context data.
    - Fix: include hold expiry and metadata in the replay contract, compare metadata semantically as JSON, and allow only microsecond timestamp tolerance for database precision.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestExistingHoldMatches|TestLedgerAccountingForHeldAndSystemDebits'` (Postgres container case skipped locally when the container runtime is unavailable).

53. Double-entry replay ignored description, metadata, and completion-state mismatches.
    - Evidence: `PostDoubleEntry` and `PostHeldDoubleEntry` treat duplicate `(tenant_id, idempotency_key)` ledger transactions as idempotent replays, but `existingDoubleEntryMatches` only compared currency, reference, wallets, and amounts. A replay with different transaction metadata, entry metadata, descriptions, or non-completed stored statuses could return success under the same idempotency key.
    - Fix: extend double-entry replay validation to require completed transaction/entry statuses, debit/credit entry types, matching descriptions, and semantically equal transaction and entry metadata.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestExistingDoubleEntryMatches|TestLedgerAccountingForHeldAndSystemDebits'` (Postgres container case skipped locally when the container runtime is unavailable).

54. Withdrawal destination usage links could count an amount or currency different from the ledger debit.
    - Evidence: `ValidateWithdrawalDestinationLinkLedgerEntry` required a debit entry and matching destination wallet, but did not compare `entry.Amount` or `entry.Currency` against the requested `LedgerWithdrawalDestinationLink`. A direct store/activity call could create a usage link that increments `withdrawal_destinations.total_withdrawn` by a different amount or currency than the actual ledger debit.
    - Fix: require withdrawal destination links to match the ledger debit amount and currency before insert/replay.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestValidateWithdrawalDestinationLinkLedgerEntry|TestFundingSourceTotalsFollowIdempotentLedgerLinks'` (Postgres container case skipped locally when the container runtime is unavailable).

55. Funding-source merges accepted contradictory source details.
    - Evidence: `UpsertFundingSource` reused an existing funding source by wallet/source/external reference, but `ValidateFundingSourceMerge` did not compare `source_details` and ignored a different incoming `withdrawal_method` once one was already stored. The update path then returned the old row, silently hiding provider/account-detail contradictions under one funding-source identity.
    - Fix: treat funding-source details as part of the merge contract and reject mismatched existing withdrawal methods while still allowing an initially empty method to be filled later.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestValidateFundingSourceMerge|TestFundingSourceTotalsFollowIdempotentLedgerLinks'` (Postgres container case skipped locally when the container runtime is unavailable).

56. PSP transaction create replays ignored raw request mismatches.
    - Evidence: `ValidatePSPTransactionCreateReplay` compared provider, idempotency key, client reference, direction, amount, currency, fees, provider transaction ID, and workflow ID, but not `raw_request`. The gRPC deposit/withdrawal handlers also checked existing transactions before constructing `raw_request`, so a retry with the same idempotency key but different metadata/options could be accepted as the existing workflow.
    - Fix: include semantic `raw_request` equality in PSP create replay validation and build deposit/withdrawal raw requests before the existing-transaction replay check.
    - Tests: `go test -count=1 ./wallet/store ./wallet/grpc`; `go test -count=1 -v ./wallet/store -run 'TestValidatePSPTransactionCreateReplay|TestPSPTransactionPersistenceReplaysAndStatusUpdates'`; `go test -count=1 -v ./wallet/grpc -run 'TestRequestDepositDoesNotGenerateProviderTransactionID|TestRequestWithdrawalRequiresPin|TestRequestWithdrawalStartsWorkflow'` (Postgres container cases skipped locally when the container runtime is unavailable).

57. PSP status updates could overwrite the provider transaction ID.
    - Evidence: `UpdatePSPTransactionStatus` used `psp_transaction_id = COALESCE(?, psp_transaction_id)`, so any later webhook/poller update with a different provider transaction ID replaced the stored ID. That hides provider contradictions and can split reconciliation/status matching.
    - Fix: validate status updates against the existing row before writing, preserve valid terminal-transition behavior, allow filling an empty provider transaction ID once, and reject mismatched later IDs as `ErrDuplicateTransaction`.
    - Tests: `go test -count=1 ./wallet/store ./wallet/workflow ./wallet/handler`; `go test -count=1 -v ./wallet/store -run 'TestValidatePSPStatusUpdate|TestUpdatePSPTransactionStatusValidation|TestPSPTransactionPersistenceReplaysAndStatusUpdates'` (Postgres container case skipped locally when the container runtime is unavailable).

58. PSP method catalog pagination ran before scoped eligibility.
    - Evidence: `ListAvailablePSPMethods` selected only active base PSP config rows with `LIMIT/OFFSET` before resolving scoped overrides and before applying direction, currency, region, and amount filters. That could return an empty or short page even when eligible methods existed later in provider order, and it hid methods that are enabled only by a matching scoped override.
    - Fix: resolve each tenant PSP config for the requested scope, apply active/direction/currency/region/amount eligibility first, then paginate the eligible method list.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestAvailablePSPMethodsFromConfigsPaginatesAfterEligibility|TestMergePSPConfigOverrideCanActivateScopedMethod|TestListAvailablePSPMethodsValidation'`; `go test -count=1 -v ./wallet/store -run 'TestListAvailablePSPMethodsPaginatesAfterScopedEligibility|TestPSPTransactionPersistenceReplaysAndStatusUpdates'` (Postgres container cases skipped locally when the container runtime is unavailable).

59. PSP configs without enabled currencies were treated as global providers.
    - Evidence: `ValidatePSPConfig` returned nil when `EnabledCurrencies` was empty, and `methodSupportsCurrency` treated an empty configured currency list as supporting every requested currency. A provider config or scoped override missing its explicit currency contract could therefore process or appear for currencies it never declared.
    - Fix: require PSP configs to declare at least one enabled currency before provider validation or catalog exposure, and map PSP validation failures to explicit API status codes instead of falling through as internal errors.
    - Tests: `go test -count=1 ./wallet/validation ./wallet/store ./wallet/grpc`; `go test -count=1 -v ./wallet/validation -run 'TestValidatePSPConfigRequiresExplicitCurrency|TestValidatePSPConfigRequiresConfiguredCurrencies|TestValidatePSPConfigMatchesTrimmedCurrency'`; `go test -count=1 -v ./wallet/store -run 'TestAvailablePSPMethodsFromConfigsRequiresConfiguredCurrencies|TestAvailablePSPMethodsFromConfigsPaginatesAfterEligibility|TestMergePSPConfigOverrideCanActivateScopedMethod'`; `go test -count=1 -v ./wallet/grpc -run 'TestMapErrorMapsPSPValidationFailures'`.

60. Deposit validation skipped wallet currency.
    - Evidence: `ValidateDeposit` called `validateWallet(wallet, "", ...)`, so a deposit request in `AED` could pass status/owner checks against a `USD` wallet and only fail later when the workflow tried to post an `AED` ledger credit into that wallet. That creates PSP transaction/workflow side effects before the currency mismatch is rejected.
    - Fix: validate the deposit wallet against the requested deposit currency at the validation boundary.
    - Tests: `go test -count=1 ./wallet/validation`; `go test -count=1 -v ./wallet/validation -run 'TestValidateDepositRequest|TestValidateDepositWalletRequiresCurrencyMatch'`.

61. Withdrawal validation made its FX conversion path unreachable.
    - Evidence: `ValidateWithdrawal` first required the wallet currency to equal the requested payout currency, then called `convertWithdrawalAmount(req.Currency, wallet.Currency)` and returned wallet/payout currency fields. Any legitimate cross-currency payout failed before rate lookup and wallet-debit calculation.
    - Fix: validate withdrawal wallets for active status and owner only, then use the existing conversion path to compute the wallet-currency debit.
    - Tests: `go test -count=1 ./wallet/validation`; `go test -count=1 -v ./wallet/validation -run 'TestValidateWithdrawalRequest|TestValidateWithdrawalWalletAllowsPayoutCurrencyMismatch|TestConvertWithdrawalAmountUsesRateLookup|TestConvertWithdrawalAmountSameCurrencySkipsRateLookup'`.

62. Terminal PSP status replays could rewrite settlement evidence.
    - Evidence: `UpdatePSPTransactionStatus` used `COALESCE` updates for response code, response message, raw response, and `confirmed_at`, while `ValidatePSPStatusUpdate` only protected the provider transaction ID. A same-status terminal replay could therefore change the stored response evidence or confirmation timestamp without changing the transaction status.
    - Fix: allow terminal status replays to fill missing evidence once, but reject contradictory existing response code/message/raw response/confirmation time as duplicate transaction conflicts.
    - Tests: `go test -count=1 ./wallet/store`; `go test -count=1 -v ./wallet/store -run 'TestValidatePSPStatusUpdate|TestUpdatePSPTransactionStatusValidation'`; `go test -count=1 -v ./wallet/store -run 'TestPSPTransactionPersistenceReplaysAndStatusUpdates'` (Postgres container case skipped locally when the container runtime is unavailable).

63. Device-token updates created partial users in the store layer.
    - Evidence: `Store.UpsertDeviceToken` updated by mobile, then created a new user with only mobile/username/device token when no row matched. A lower-layer device-token write could therefore manufacture an identity record instead of failing on a missing user.
    - Fix: require explicit mobile and token values in the store and return `sql.ErrNoRows` when the target user does not already exist.
    - Tests: `go test -count=1 ./store ./consumer`; `go test -count=1 -v ./store -run 'TestStore_UpsertDeviceTokenRequiresExplicitFields|TestStoreTargetedUpdatesReportMissingRows|TestStoreTenantValidation'` (Postgres container case skipped locally when the container runtime is unavailable); `go test -count=1 -v ./consumer -run 'TestAddDeviceTokenRequiresExplicitInputs|TestUserServiceTenantValidationFailsBeforeDB'`.

## Open Candidates

No open candidates in this file yet after the current pass. Continue scanning the repo for remaining TODO/FIXME markers, silent defaults, hidden errors, dead paths, and tooling gaps.
