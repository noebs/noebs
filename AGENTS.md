# Agent Engineering Guidelines

These rules apply to all agents working in this repo. The intent is to keep layers clean and deterministic. Do **not** “defensively” mutate inputs inside lower layers.

## Layer Responsibilities

- **API/Handler layer**: validate request shape, apply defaults from config, and convert to domain types. Reject missing required fields here.
- **Domain/Service layer**: assume validated inputs; enforce business rules. Do not fill in missing tenant/currency/user identifiers.
- **Store/DB layer**: never guess or default identifiers. Inputs must be explicit; return errors on invalid/missing values.

## No Silent Defaults

**Forbidden (example):**
```
if tenantID == "" { tenantID = basestore.DefaultTenantID }
if currency == "" { currency = "USD" }
if userID != nil { uid = sql.NullInt64{Int64:*userID, Valid:true} }
```

**Required pattern:**
- If a field is required, the function must return a typed error when empty/invalid.
- Defaults must be applied **once** at the boundary (handler/config layer), not in shared stores or services.

## Error Contracts

- Use typed errors for validation failures (e.g., `ErrMissingTenantID`, `ErrMissingCurrency`, `ErrInvalidUserID`).
- Do not swallow or transform errors into defaults.
- Prefer explicit parameter objects/structs over optional pointers when values are required.

## Testing Expectations

- Add tests that assert missing/empty inputs fail fast in the correct layer.
- Include tests to ensure defaults are applied only at the boundary, not in lower layers.

## Rationale

Silent defaulting changes behavior, hides bugs, and makes system behavior nondeterministic across layers. Each layer must do exactly its job and nothing more.
