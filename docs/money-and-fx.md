# Money and FX architecture

This subsystem has two deliberately separate jobs:

1. `groosh` gives an integer amount meaning by binding it to one immutable currency-unit version and performs exact arithmetic, parsing, formatting, allocation, cash quantization, and conversion.
2. The FX pipeline retrieves attributable external observations, stores their provenance, and produces expiring **reference previews**. It does not create executable dealing prices.

No store or service invents a tenant, currency, unit version, user, scale, rate side, or rounding mode. Handlers validate boundary input; lower layers reject missing values with typed errors.

## Currency units and scales

`currencies` is the currency catalog. `currency_unit_versions` is the authoritative, effective-dated definition of each unit. A unit version has a half-open validity interval `[valid_from, valid_to)` and these scale fields:

- `iso_minor_exponent`: the operational ledger scale. A major unit contains `10^iso_minor_exponent` minor units. A definition without this metadata may be listed, but cannot construct `groosh.Money`.
- `display_exponent`: the number of decimal places used when rendering for a person. It never changes the stored ledger amount.
- `cash_exponent` and `cash_rounding_increment`: the physical-cash quantum. In operational minor units the quantum is `cash_rounding_increment * 10^(iso_minor_exponent-cash_exponent)`.

The catalog is database-defined because scales and even currencies change over time. ISO 4217 data should come from the [SIX currency-code lists](https://www.six-group.com/en/products-services/financial-information/market-reference-data/data-standards.html); display and cash conventions can be reviewed against the [Unicode number-data specification](https://unicode.org/reports/tr35/tr35-numbers.html) and the exact [CLDR 48 stable release line](https://cldr.unicode.org/downloads/cldr-48). Neither source is silently consulted at runtime. The initial catalog is pinned to SIX's 2026-01-01 list and stable CLDR 48.2.1, published 2026-07-08, and deliberately does not import CLDR 49 development data.

MRU is the one seed whose cash quantum has additional primary-source provenance. A [Mauritanian government decree implementing Ordinance 2017-001](https://ami.mr/fr/archives/65359) establishes a 1/5-ouguiya coin as legal tender from 2018, so the catalog records a cash increment of 0.20 MRU (20 operational minor units). That independently reviewed rule does not depend on an unreleased CLDR override. A later authoritative correction must be a new reviewed unit version, never an edit to the seed's provenance.

Unit versions cannot be deleted and are immutable after insertion, except for the first closure of the current version. A closure must be future-dated and committed in the same transaction as one open-ended successor beginning on exactly `valid_to`; validity intervals cannot overlap. Wallets, ledger transactions, and ledger entries pin `currency_unit_version_id`, so historical amounts retain their original meaning.

The current wallet identity is one row per tenant/owner/currency, and double-entry postings require both sides to use the same exact unit version. The database therefore rejects a successor as soon as any wallet exists for that currency. A transition may be staged only before the first wallet is admitted; while such a future transition is pending, wallet creation is paused until the open-ended successor becomes effective. The advisory-lock and trigger checks serialize both orderings of the first-wallet/transition race, including direct SQL. An established currency needs a separately reviewed redenomination workflow that moves every user and system wallet atomically before this restriction can be relaxed. Never interpret effective dating as permission to split a live ledger across unit versions, even for a display-only change.

`currencies.is_active` is admission control, not a balance freeze. Disabling a currency prevents new wallet selection, public parsing, and new reference quotes in that unit. Historical rendering and already-pinned wallet/workflow operations continue to use their immutable unit identity; suspending those operations requires a separate, explicit business control.

## `groosh` invariants

`groosh.Money` is a signed `int64` minor-unit count plus an immutable `CurrencyUnit` snapshot. Its zero value is invalid; construct it with `NewMoney`, `ParseMajor`, or `ParseCanonical`.

- Addition, subtraction, comparison, and allocation require complete unit-version equality and detect `int64` overflow.
- `ParseMajor` is locale-independent and exact. It accepts an optional sign, ASCII digits, and one decimal point; it rejects whitespace, grouping, symbols, exponent notation, and non-zero precision below the operational minor unit.
- `MajorString` is the exact fixed operational scale. `CanonicalString` adds identity, for example `USD@12 10.25`. `MinorString` is intended for JSON boundaries.
- `Display` refuses an inexact scale reduction. `DisplayRounded` requires one of `half_even`, `half_away_from_zero`, `toward_zero`, `floor`, or `ceiling`.
- `QuantizeCash` applies the catalog's cash quantum with an explicit rounding mode.
- `Convert` uses an exact positive `big.Rat` quote-major-per-base-major rate, adjusts both operational scales, rounds once at the target minor-unit boundary, and checks overflow.
- Percentage fees are evaluated as exact rationals and use the named server policy `half_away_from_zero` at the minor-unit boundary; midpoint behavior is covered by tests rather than inherited implicitly from a decimal library.

New `MoneyAmount`, quote, and currency-unit JSON fields emit monetary integers and unit-version IDs as decimal strings so values beyond JavaScript's exact integer range remain lossless. Existing unversioned wallet fields remain JSON numbers for wire compatibility during their deprecation window; frontend code should move to the nested exact `*_money` fields. Do not add floating-point fields to new money or rate contracts.

## Observation and quote versioning

An FX observation and a conversion quote pin different facts:

- An observation pins the base and quote unit versions effective at `observation_at`, plus source, pair, side, purpose, exact decimal rate, publication/retrieval/expiry times, source revision, and SHA-256 of the payload.
- A quote preserves those observation unit IDs and requires separate, explicit input/output unit-version IDs effective at conversion time. This matters across scale changes: the historical observation retains its original meaning while the new amount is expressed in the units selected for the quote.
- A quote expires exactly when its observation expires. Retrieval never extends an observation's life.

`raw_payload_sha256` detects whether replayed provider bytes match; it is not a payload archive. Deployments that require independent later reconstruction must retain the provider response in a separately secured, retention-governed evidence store and bind it to the same hash.

Rates are stored as bounded exact decimals and converted to rational numbers for arithmetic. Direct and inverse lookup are explicit. Inverting a bid uses the stored ask; inverting an ask uses the stored bid; mid remains mid. The persisted observation side plus the quote's inverse flag losslessly derive the requested side; responses expose both.

Public quote creation always uses server policy `half_even`. Clients cannot supply quote rounding. A positive input may legitimately round to zero target minor units; a reference preview preserves that exact result, while executable settlement paths reject a zero credit. Sources currently have purpose `reference`, so responses set `executable=false`. The [ECB itself says its reference rates are informational and strongly discourages transaction use](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html). A future executable source needs a separately reviewed purpose, pricing/markup rules, acceptance semantics, and settlement workflow; relabeling a reference source is not sufficient.

## FX catalog and provider trust boundary

`fx_sources`, `fx_source_pairs`, and `fx_source_pair_sides` define what may be fetched and quoted. `GET /wallet/fx/sources` exposes the enabled source/pair/side matrix so clients do not guess combinations. Disabled catalog snapshots are rejected by providers even if a caller bypasses the normal enabled-only store query.

Current providers are:

- `ecb_sdmx`: ECB daily euro reference rates. The configured series must be exactly `D.<quote>.<base>.SP00.A`; returned key and orientation must match, the selected date must occur once, and `OBS_STATUS` must be `A` (normal). ECB's [SDMX tutorial documents the series dimensions and normal status](https://www.ecb.europa.eu/stats/ecb_statistics/sdmx/html/tutorial.en.html). The status remains in `source_revision` for audit.
- `cbos_html`: Central Bank of Sudan reference table. The parser requires one dated table with the exact known headers, unique currency rows, positive ordered bid/mid/ask values, and SDG quote orientation. Treat an upstream HTML change as a parser failure requiring review, not as permission to guess columns.

Both implementations pin an exact HTTPS host and base path. Redirects are not followed. The response's recorded final scheme, host, escaped path, and query must exactly equal the request before any body is parsed. Status, media type, body size, schema, orientation, duplicates, future dates, and rates are fail-closed checks.

`max_age_seconds` is mandatory catalog policy. Expiry is `observation_at + max_age_seconds`, not `retrieved_at + max_age_seconds`. ECB normally publishes each working day except TARGET closing days, so its policy must cover weekends and multi-day closures; a seven-day budget covers the ordinary Good Friday/Easter Monday gap described by the [ECB publication schedule](https://www.ecb.europa.eu/stats/exchange_rates/html/index.en.html). An expired observation remains auditable but is not quotable. Never fall back to it silently when a publisher delays or suspends a series.

## Temporal ingestion

The service starts `FXReferenceSync` as workflow ID `wallet-fx-reference-sync` on task queue `wallet-main`, using the required `wallet_fx_refresh_cron` setting. The deployed schedule is `30 16 * * 1-5`; Temporal cron interpretation and cluster timezone must be verified when changing it. An already-started Temporal cron execution retains its original schedule: changing configuration alone does not rewrite that execution. A schedule change therefore needs a reviewed terminate-and-recreate operation (or a new versioned workflow ID) after confirming that no ingestion activity is in flight.

Each run:

1. Calls `ListEnabledFXSources` with a 30-second start-to-close timeout and five-attempt exponential retry policy.
2. Sorts and validates unique, non-empty source codes inside workflow code.
3. Schedules every `SyncFXSource` activity in that canonical order without waiting between sources. ECB pair requests use bounded four-way concurrency inside the activity, keeping the four seeded series within the two-minute start-to-close budget even when individual HTTP calls approach their 30-second timeout. Activities have a five-attempt exponential retry policy.
4. Collects futures into their sorted result slots. The first completed failure fails the workflow and requests cancellation of peers; no partial workflow result is returned.

Already-started remote activities may finish before cancellation is observed. This is safe because observation insertion is idempotent on source provenance and verifies that a replay carries the same material data. `retrieved_at` is captured only after all provider responses for the activity have completed, so it is the earliest claimed network-availability time for that attempt. A retry may therefore present a later value; the first successfully stored completion time remains authoritative. `created_at` records database availability. Historical selection and every quote or PSP provenance check require `observation_at`, `retrieved_at`, and `created_at` to be no later than the conversion boundary, while `expires_at` must be later. Rate, unit versions, publisher times, expiry, payload hash, source revision, pair, side, and purpose must still match or replay fails instead of overwriting history. A failed workflow can therefore be retried without deleting completed observations.

The activity resolves observation unit versions at each observation's UTC date, never at retrieval time. Provider/schema/catalog errors are non-retryable; temporary HTTP/server failures remain retryable.

Operational checks:

```sql
SELECT source.code,
       max(observation.observation_at) AS latest_observation,
       max(observation.retrieved_at) AS latest_retrieval,
       bool_or(observation.expires_at > now()) AS has_fresh_observation
FROM fx_sources source
LEFT JOIN fx_observations observation ON observation.source_id = source.id
WHERE source.is_enabled
GROUP BY source.code
ORDER BY source.code;
```

Alert on failed/stuck `wallet-fx-reference-sync` runs, enabled sources without a fresh observation, repeated provider schema errors, and quote-not-found spikes. Inspect the Temporal workflow history and worker logs before retrying. Do not repair freshness by editing timestamps or rates.

## Public API

The authenticated public gRPC service and its HTTP gateway expose:

- `ListCurrenciesPublic` / `GET /wallet/currencies`
- `GetCurrencyPublic` / `GET /wallet/currencies/{currency_code}`
- `ParseMoneyPublic` / `POST /wallet/money/parse`
- `FormatMoneyPublic` / `POST /wallet/money/format`
- `ListFXSourcesPublic` / `GET /wallet/fx/sources`
- `QuoteConversionPublic` / `POST /wallet/fx/quotes`
- `GetConversionQuotePublic` / `GET /wallet/fx/quotes/{quote_id}`

Tenant and user identity come from authenticated gateway claims; body/query overrides are rejected. Parse and format requests require an exact `currency_unit_version_id`; quote requests require exact base and quote unit IDs plus an opaque `idempotency_key`. Reusing a key for the same semantic request returns the original quote even after it expires; reusing it with different amount, pair, source, side, or unit identity is a conflict. New rows are capped by the explicit `wallet_fx_quote_max_per_user_observation` configuration. The store serializes the per-user/observation count with PostgreSQL transaction advisory locks across replicas, and the matching index keeps the count bounded; retries are looked up before quota accounting. Parsing and formatting also require an explicit rounding mode for the returned display representation. FX quote rounding is deliberately absent from the request. `MoneyAmount` returns minor units, operational exponent, exact major units, display text, canonical version-bound text, and currency-unit version ID. Public list limits are capped at 500 and offsets at 100,000.

## Adding a provider or catalog entry

1. Add source, pair, and allowed-side rows through a reviewed migration. Set explicit purpose, pinned URL, and observation-based freshness; never add a code default in service/store logic.
2. Implement `Provider.Fetch` with a provider-specific endpoint allowlist and strict source/pair validation. Return canonically sorted observations.
3. Reject redirects and validate the final response URL before reading. Bound the body, require exact media/schema/orientation, reject ambiguity, and retain payload hash plus publisher revision.
4. Register the provider name in `NewDefaultRegistry` and wire its worker dependency.
5. Add fixtures for valid data, malformed schema, duplicates, wrong orientation, future dates, stale quote behavior, redirect/final-URL attacks, disabled catalog rows, temporary errors, and body limits.
6. Exercise a Temporal test run and confirm unit resolution at `observation_at`, idempotent replay, inverse side behavior, expiry, discovery output, and non-executable quote status.
7. Deploy the catalog migration before enabling the source. Watch at least one scheduled ingestion and query before exposing the pair to clients.
