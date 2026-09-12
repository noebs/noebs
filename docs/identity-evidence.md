# Account identity intake and manual review

NoEBS owns the customer's document application, its evidence and its review
outcome. The authenticated account can capture a passport or national ID, submit
it for manual review, recover its current status on another installation, supply
a new application when information is requested, and withdraw an application.
The review team can inspect the submitted claims and images, then record an
approved, needs-information or rejected decision with a customer-facing reason.

This capability is independent of the selected payment provider. Identity media
and document numbers are not sent to Mojaloop. Manual approval does not change
wallet KYC tiers, balances or payment permissions, or assert an automated
liveness/authenticity result.

## Customer API

Every operation uses the normal mobile OIDC bearer token and tenant header.
The gateway resolves the account owner; owner, issuer, subject and tenant cannot
be supplied in the body. IDs are canonical client-generated UUIDs retained
through retries. Responses use `Cache-Control: no-store`.

| Method | Path | Request |
| --- | --- | --- |
| POST | `/consumer/identity/sessions` | `session_id`, `document_type` (`passport` or `national_id`), optional `previous_session_id` for a correction |
| GET | `/consumer/identity/sessions/latest` | Latest owned customer case; 404 when none exists |
| GET | `/consumer/identity/sessions/:session_id` | Owned case with evidence hashes and status |
| PUT | `/consumer/identity/sessions/:session_id/evidence/:kind` | Raw JPEG, `Content-Type: image/jpeg`, current `X-Identity-Revision` |
| POST | `/consumer/identity/sessions/:session_id/submit` | `revision`, `consent_version: "identity-review-v1"`, `fields_reviewed: true`, `holder_name`, optional `document_number` |
| POST | `/consumer/identity/sessions/:session_id/withdraw` | `revision` |
| DELETE | `/consumer/identity/sessions/:session_id` | Current `X-Identity-Revision`; discards a draft (legacy contract) |

Creation no longer requires a test-data declaration. The old optional
`synthetic` boolean remains accepted and stored as historical provenance;
omission means `false`. Replaying creation never rewrites that marker. Existing
marked cases remain readable by their owner and authorized operators, but are
excluded from the customer latest-case lookup and review queue and cannot
receive an approved decision. The new app clears old local demonstration drafts
and requires a fresh capture; changing a flag must never relabel old fixtures.

The new consent version describes account review. Legacy `identity-evidence-v1`
is accepted only for historical marked sessions; it cannot submit a new customer
application. Previously submitted consent receipts are never rewritten.

Evidence kinds are `document_front`, `selfie`, and optional `document_back` for
national ID. The national-ID front supports a full A4 paper certificate; passport
number and gender are not capture prerequisites. Each image is a JPEG of at most
2 MiB, 64–6000 pixels per dimension and 12 million pixels. The boundary decodes
it after bounding dimensions. Encoding validation alone does not establish the
content's authenticity, portrait match or liveness.

Responses contain `session_id`, `document_type`, `status`, `revision`,
timestamps, legacy `synthetic`, and `evidence: [{kind, sha256, bytes}]`. Submitted
or decided cases include the reviewed `submission`. A decided case additionally
includes:

```json
{
  "status": "needs_information",
  "review_status": "needs_information",
  "liveness_status": "not_evaluated",
  "document_status": "not_evaluated",
  "review": {
    "decision": "needs_information",
    "method": "manual",
    "reason": "Please capture the entire certificate with all four corners visible.",
    "reviewed_at": "2026-09-12T12:00:00Z"
  }
}
```

States are `draft`, `submitted`, `approved`, `needs_information`, `rejected`,
`discarded` and `withdrawn`. Submitted customer cases have
`review_status: "pending_review"`. The review method, customer-facing reason and
decision time are durable; the operator's identity, database login, policy
reference, operation ID and evidence hashes are retained in the restricted audit
log. `liveness_status` and `document_status` remain `not_evaluated`, including
on a manual approval. No external verifier has been configured by this release.

## Recovery, corrections and withdrawal

Creation is idempotent for an owner, UUID, document type and preceding-case link.
An upload locks its case row and requires the current revision. A stale changed
capture returns `409 identity_conflict`. Repeating the immediately preceding
upload with identical bytes succeeds after a lost response; otherwise fetch the
case and compare hashes before retransmission. Images upload independently.

Submission requires the current revision, reviewed claims, matching consent,
document front and selfie. It freezes the evidence set atomically. Exact retries
return the current receipt even if the case has subsequently been reviewed;
changed terms and further image uploads are rejected.

When more information is needed, create a new session linked by
`previous_session_id`. The previous case must belong to the same owner and have
status `needs_information` or `rejected`. The old submission, evidence and review
remain immutable while the new case is captured and submitted. Replacing images
inside a decided case is forbidden.

Latest-case lookup excludes discarded drafts and historical test-marked cases.
It includes the newest withdrawn tombstone so withdrawal cannot cause an older
approval or submission to reappear as the current application.

Withdrawal is an authenticated, revision-checked operation for an existing
application. In one database transaction it removes stored image bytes and
submitted name/document claims, clears the case's review payload, records a
withdrawal event, and keeps a `withdrawn` tombstone. The immutable event history
retains account/case identifiers, hashes, access records and any recorded review
reason/policy. It does not copy uploaded images or submitted claims. Late uploads
and submissions cannot recreate withdrawn evidence. Exact withdrawal retries
succeed; concurrent review and withdrawal have one revision winner. An operator's
exact decision retry after withdrawal returns the current withdrawn state and
never restores approval or evidence.

Draft `DELETE` keeps its prior behavior: it deletes images and leaves a discarded
tombstone. Unknown and other owners' cases both return 404. Conflicts return 409,
missing required evidence 422, malformed fields 400, wrong media type 415, and
oversized images 413. Uploads resume per-image, not from a byte offset.

## Operator review commands

The shipped `noebs identity-review` command is the operator entrypoint. It opens
only the existing `identity_auth_runtime` login and `identity_auth` database from
an explicit runtime secrets file, verifies the configured database TLS CA and
session authority, and requires a tenant and accountable reviewer label. It does
not initialize payment, gateway or other service runtimes. No public reviewer
endpoint or extra client-side approval capability is exposed.

Host/container execution rights and possession of the identity runtime database
credentials authorize this operation. `--reviewer` is the operator's explicit
accountability assertion; it is not a separate authenticated OIDC reviewer
identity. PostgreSQL independently records `session_user` and event time. The
runtime cannot update/delete audit events or override those server-recorded
columns. Restrict command execution to the designated review team.

Use the installed `/usr/local/bin/noebs` inside the authorized identity workload,
or a matching binary on an authorized operator host with database connectivity.
Examples below use shell variables for the actual tenant, case, user and reviewer;
keep runtime secrets and exports in `.state/` on the operator host. The mounted
runtime secret inside the identity workload is `/app/secrets.yaml`.

```sh
noebs identity-review --action queue --secrets "$review_secrets" \
  --tenant "$review_tenant" --reviewer "$review_actor" --limit 50 --offset 0

noebs identity-review --action case --secrets "$review_secrets" \
  --tenant "$review_tenant" --reviewer "$review_actor" \
  --session "$review_case" --user-id "$review_user" \
  --output .state/identity-review/case.json

noebs identity-review --action evidence --secrets "$review_secrets" \
  --tenant "$review_tenant" --reviewer "$review_actor" \
  --session "$review_case" --user-id "$review_user" --revision "$review_revision" \
  --kind document_front --output .state/identity-review/document-front.jpg
```

Read the private case JSON and each image using the approved review workstation.
Repeat the evidence command for `selfie` and any `document_back`, using new output
paths. Each successful case/image read commits an audit record before returning
the private content. Evidence exports require the current submitted/decided case
revision. All evidence in the case must have been accessed by the same reviewer
at the reviewed revision before a decision can be recorded. Opening/exporting an
image is not itself an approval: the operator must actually inspect the supplied
claims and evidence under the tenant's defined policy.

Store a short customer-facing explanation in a private reason file; do not repeat
document numbers or biometric details in that explanation. Retain one generated
UUID in `review_operation` through decision retries and supply the exact policy
version applied by the operator:

```sh
noebs identity-review --action decide --secrets "$review_secrets" \
  --tenant "$review_tenant" --reviewer "$review_actor" \
  --session "$review_case" --user-id "$review_user" --revision "$review_revision" \
  --operation-id "$review_operation" --decision needs_information \
  --reason-file .state/identity-review/customer-reason.txt \
  --policy "$review_policy" --evidence-reviewed
```

The allowed decisions are `approved`, `needs_information` and `rejected`. Decisions
require an explicit reason, policy reference and review attestation, plus the
recorded access above. Same-key retries preserve the first decision; changed
terms or a second terminal decision fail. The command prints only identifiers,
status and revision for a decision. Queue output contains case/owner identifiers,
revision and submission time, not names/document numbers. Case and image output
must be a new file below `.state/`; files are created with mode `0600`, symbolic
link traversal and overwriting existing files are rejected, and a failed export
is removed. Delete workstation exports under the review team's media-handling
procedure when they are no longer needed.

## Migration, rollout and operating prerequisites

Additive identity migration `003_identity_review.sql` preserves existing rows,
relaxes the prior test-only admission constraint, adds review states and
preceding-case links, and creates the restricted audit table. Apply the standard
identity migration and its privilege reconciliation before rolling out the
identity service and gateway, then the app. Migration `002`, legacy `/consumer/kyc`,
passport storage and payment ledgers retain their existing contracts. The old
binary can read existing contracts but cannot run this review workflow; a schema
rollback that discards durable review decisions is deliberately prohibited.

This release supplies a usable manual review workflow. Before customer operation,
the deployment owner still needs to assign and authorize reviewers, define the
accepted document variants and manual evidence policy, provide truthful consent
and retention wording, and confirm the deployed data-handling requirements. The
command records the chosen policy; it cannot establish that a policy meets a
bank's or jurisdiction's requirements. Automatic identity-provider checks,
reviewer web UI, automatic wallet-tier upgrades and production Mojaloop transport
onboarding are separate capabilities.

Images remain in the identity PostgreSQL database under the existing database
access/TLS boundary. Withdrawal deletes them from primary tables, but it cannot
erase database backups or already exported operator files. No automatic evidence
expiry, backup purge, legal-hold classification or retention schedule is claimed
by this implementation. Configure and exercise those operating procedures for
the selected deployment rather than treating a customer withdrawal receipt as
an all-copies deletion certificate.

## Verification

Use only the isolated test database (never a deployment database):

```sh
source .state/postgres/test.env
PATH="$PWD/.state/bin:$PATH" go test -race ./store ./consumer/handler ./consumer ./cli \
  -run 'TestIdentity|TestProfileProjection|TestPostgresMigrationsAreFreshCanonicalBaselines' -count=1
```

Executed checks cover real PostgreSQL ownership and tenant isolation, existing
upload/submit races and recovery, ordinary creation and legacy provenance,
consent-version separation, latest-case recovery, correction ownership and prior
state, reviewer/evidence/revision access binding, immutable retry-safe decisions,
runtime audit write restrictions and privilege reapplication, withdrawal privacy
cleanup and decision/withdrawal races. CLI checks cover explicit authority,
actual runtime secret structure, TLS/role rejection, private exclusive files and
symlink/traversal rejection. Handler checks confirm manual approval never claims
automated liveness/document checks. Fixtures are confined to isolated tests;
these checks do not measure a human review team's accuracy or document coverage.
