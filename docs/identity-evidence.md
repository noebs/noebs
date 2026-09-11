# Identity evidence intake

This is a separate NoEBS identity capability, available to the same authenticated
profile as the app's wallet and providers. It does not send identity images,
document numbers or biometric material to Mojaloop, change a wallet's KYC tier,
or produce a verified identity. Only synthetic evidence is accepted in this
iteration. The independent [research and future design](../../mojaloop/docs/identity/README.md)
describe the provider evaluation and real identity checks still needed.

## Current HTTP contract

Every request uses the normal NoEBS mobile OIDC bearer token and explicit tenant
header. The gateway resolves the authenticated profile; a caller cannot supply
an owner, issuer, subject or tenant in the body. Session IDs are canonical UUIDs
generated once by the client and retained through retries.

| Method | Path | Body or headers |
| --- | --- | --- |
| POST | `/consumer/identity/sessions` | JSON: `session_id`, `document_type` (`passport` or `national_id`), `synthetic: true` |
| GET | `/consumer/identity/sessions/:session_id` | Read the owned session and received evidence metadata |
| PUT | `/consumer/identity/sessions/:session_id/evidence/:kind` | Raw JPEG, `Content-Type: image/jpeg`, `X-Identity-Revision` from the last session response |
| POST | `/consumer/identity/sessions/:session_id/submit` | JSON: `revision`, `consent_version: "identity-evidence-v1"`, `fields_reviewed: true`, `holder_name`, optional `document_number` |
| DELETE | `/consumer/identity/sessions/:session_id` | `X-Identity-Revision`; discard a draft and remove its stored images |

Evidence kinds are `document_front`, `selfie`, and optionally `document_back`
for national ID. The national-ID front supports a complete A4 certificate;
there is no passport-number or gender prerequisite. Document and selfie uploads
are independent so interruption does not require resending both. Each JPEG is
at most 2 MiB, 64–6000 pixels per dimension, and at most 12 million pixels. The
boundary decodes the bounded JPEG before accepting it. These are encoding
checks, not document authenticity, face matching or liveness checks.

Responses contain `session_id`, `document_type`, `synthetic`, `status`,
`revision`, timestamps, and `evidence: [{kind, sha256, bytes}]`. Submitted
sessions also include the reviewed `submission`. Responses never return image
bytes and use `Cache-Control: no-store`.

The three decision fields are explicit:

```json
{
  "status": "submitted",
  "liveness_status": "not_evaluated",
  "document_status": "not_evaluated",
  "review_status": "pending_provider"
}
```

`pending_provider` means no verification provider is connected. There is no
implemented reviewer queue, automatic approval or promised review time.
Capturing a selfie, moving one's head, recognizing text, or successfully
uploading an image never upgrades these statuses.

## Recovery and ownership

Creation is idempotent for the same owner, UUID and document type. An upload
locks its session row and requires the current revision; a changed stale
capture returns `409 identity_conflict`. Repeating the immediately preceding
upload with the same bytes also succeeds after a lost response. On any other
uncertain upload outcome, GET status and compare the received SHA-256 before
resending. Clients must not silently accept replacement evidence from an old
capture attempt or another signed-in account.

Submission requires the current revision, reviewed fields, consent version,
document and selfie. It atomically freezes the evidence set. Exact retries
return the same receipt; changed submission terms or further uploads fail.
Discard removes draft images and leaves a tombstone so late uploads cannot
recreate them. Submitted evidence cannot be discarded through the draft API.

Unknown and other owners' sessions both return 404. Conflicts return 409,
missing required evidence 422, malformed fields 400, wrong content type 415,
and oversized JPEGs 413. Uploads are not chunk-resumable: a failed individual
JPEG may need retransmission, while already accepted images remain received.

## Storage and rollout

Additive identity migration `002_identity_evidence.sql` adds owner-bound
`identity_sessions` and `identity_evidence` tables. Existing users, legacy
`/consumer/kyc`, passport records and payment ledgers retain their contracts.
The standard identity migration grants the existing identity runtime access;
payment services receive no evidence access. Old binaries can coexist with the
new tables. Deploy the API and gateway image before the client capability.

For this synthetic intake, bounded JPEG bytes live in the identity PostgreSQL
database. Database transport/storage protection follows the existing release.
An object store with per-object access, upload expiry, audited verifier access,
retention/deletion jobs, consent withdrawal, reviewer workflow and evaluated
PAD/document-verifier integration must precede real identity evidence. No
automatic retention window or deletion from database backups is claimed here.

## Executed local verification

Use the repository's isolated PostgreSQL test environment (never a deployment
database):

```sh
source .state/postgres/test.env
go test -race ./store ./consumer/handler ./consumer ./cli \
  -run 'TestIdentityEvidence|TestIdentityAuth|TestUserService|TestProfileProjection|TestAPIGatewayCatalog|TestMigrationAuthority' -count=1
```

Tests cover owner and tenant isolation, durable recovery, revision conflicts,
concurrent captures, immutable submission and same-request replay, discard,
missing-authority failures, image/body limits, and truthful receipt fields.
They use synthetic byte fixtures; they do not evaluate human liveness or
document-verification accuracy. Deployment and device evidence is recorded in
the cross-repository integration evidence after promotion.
