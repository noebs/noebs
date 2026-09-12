package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/google/uuid"
)

func identityTestOwner(t *testing.T, s *Store, tenant, subject string) IdentityOwner {
	t.Helper()
	provisionTestTenants(t, t.Context(), s,
		tenantcatalog.Tenant{ID: "tenant", Name: "Synthetic Identity Tenant"},
		tenantcatalog.Tenant{ID: "tenant-other", Name: "Other Synthetic Identity Tenant"})
	profile, err := s.CreateProfileProjection(t.Context(), CreateProfileProjectionParams{
		PrincipalIdentity: PrincipalIdentity{TenantID: tenant, Issuer: testProfileIssuer, Subject: subject},
		Fullname:          "Synthetic Test Person",
	})
	if err != nil {
		t.Fatal(err)
	}
	return IdentityOwner{TenantID: tenant, UserID: profile.UserID}
}

func newIdentityDraft(t *testing.T, s *Store, owner IdentityOwner) IdentitySession {
	t.Helper()
	result, err := s.CreateIdentitySession(t.Context(), CreateIdentitySessionParams{
		Owner: owner, SessionID: uuid.New(), DocumentType: "national_id", Synthetic: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func identityUpload(t *testing.T, s *Store, owner IdentityOwner, draft IdentitySession, kind string) IdentitySession {
	t.Helper()
	result, err := s.PutIdentityEvidence(t.Context(), PutIdentityEvidenceParams{
		Owner: owner, SessionID: draft.ID, Revision: draft.Revision, Kind: kind, JPEG: []byte("synthetic-" + kind),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestIdentityEvidenceSubmissionIsDurableAndImmutable(t *testing.T) {
	ctx := t.Context()
	s := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, s, "tenant", "identity-test-owner")
	draft := newIdentityDraft(t, s, owner)
	replay, err := s.CreateIdentitySession(ctx, CreateIdentitySessionParams{
		Owner: owner, SessionID: draft.ID, DocumentType: "national_id", Synthetic: true})
	if err != nil || replay.ID != draft.ID || replay.Revision != 1 {
		t.Fatalf("create replay = %+v, %v", replay, err)
	}
	if _, err := s.CreateIdentitySession(ctx, CreateIdentitySessionParams{
		Owner: owner, SessionID: draft.ID, DocumentType: "passport", Synthetic: true}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("changed document kind = %v", err)
	}
	submission := IdentitySubmission{Revision: draft.Revision, ConsentVersion: IdentityConsentVersion,
		FieldsReviewed: true, HolderName: "Synthetic Person", DocumentNumber: "SYNTHETIC-001"}
	if _, err := s.SubmitIdentitySession(ctx, owner, draft.ID, submission); !errors.Is(err, ErrIdentityIncomplete) {
		t.Fatalf("empty submission = %v", err)
	}
	draft = identityUpload(t, s, owner, draft, "document_front")
	draft = identityUpload(t, s, owner, draft, "selfie")
	submission.Revision = draft.Revision
	result, err := s.SubmitIdentitySession(ctx, owner, draft.ID, submission)
	if err != nil || result.Status != "submitted" || len(result.Evidence) != 2 || result.Revision != 4 {
		t.Fatalf("submission = %+v, %v", result, err)
	}
	// A new Store instance models process loss; status comes from PostgreSQL.
	restarted := &Store{DB: s.DB}
	recovered, err := restarted.GetIdentitySession(ctx, owner, draft.ID)
	if err != nil || recovered.Status != "submitted" || len(recovered.Submission) == 0 {
		t.Fatalf("recovery = %+v, %v", recovered, err)
	}
	replayed, err := s.SubmitIdentitySession(ctx, owner, draft.ID, submission)
	if err != nil || replayed.Revision != result.Revision {
		t.Fatalf("lost response retry = %+v, %v", replayed, err)
	}
	submission.HolderName = "Replacement"
	if _, err := s.SubmitIdentitySession(ctx, owner, draft.ID, submission); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("changed retry = %v", err)
	}
	if _, err := s.PutIdentityEvidence(ctx, PutIdentityEvidenceParams{Owner: owner, SessionID: draft.ID,
		Revision: result.Revision, Kind: "selfie", JPEG: []byte("replacement")}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("replace submitted evidence = %v", err)
	}
	if _, err := s.DiscardIdentitySession(ctx, owner, draft.ID, result.Revision); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("discard submitted = %v", err)
	}
}

func TestIdentityEvidenceOwnerAndRevisionIsolation(t *testing.T) {
	ctx := t.Context()
	s := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, s, "tenant", "identity-first")
	other := identityTestOwner(t, s, "tenant", "identity-other")
	otherTenant := identityTestOwner(t, s, "tenant-other", "identity-first")
	draft := newIdentityDraft(t, s, owner)
	for _, intruder := range []IdentityOwner{other, otherTenant} {
		if _, err := s.GetIdentitySession(ctx, intruder, draft.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cross-owner read = %v", err)
		}
		if _, err := s.PutIdentityEvidence(ctx, PutIdentityEvidenceParams{Owner: intruder, SessionID: draft.ID,
			Revision: 1, Kind: "selfie", JPEG: []byte("intruder")}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cross-owner upload = %v", err)
		}
		if _, err := s.DiscardIdentitySession(ctx, intruder, draft.ID, 1); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("cross-owner discard = %v", err)
		}
	}
	draft = identityUpload(t, s, owner, draft, "document_front")
	params := PutIdentityEvidenceParams{Owner: owner, SessionID: draft.ID, Revision: 1,
		Kind: "document_front", JPEG: []byte("synthetic-document_front")}
	replay, err := s.PutIdentityEvidence(ctx, params)
	if err != nil || replay.Revision != 2 {
		t.Fatalf("upload response-loss retry = %+v, %v", replay, err)
	}
	params.JPEG = []byte("stale replacement")
	if _, err := s.PutIdentityEvidence(ctx, params); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("stale replacement = %v", err)
	}
	discarded, err := s.DiscardIdentitySession(ctx, owner, draft.ID, draft.Revision)
	if err != nil || discarded.Status != "discarded" || len(discarded.Evidence) != 0 {
		t.Fatalf("discard = %+v, %v", discarded, err)
	}
	if _, err := s.DiscardIdentitySession(ctx, owner, draft.ID, draft.Revision); err != nil {
		t.Fatalf("discard response-loss retry = %v", err)
	}
	if _, err := s.PutIdentityEvidence(ctx, params); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("recreate discarded evidence = %v", err)
	}
}

func TestIdentityEvidenceConcurrentCaptureHasOneWinner(t *testing.T) {
	ctx := t.Context()
	s := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, s, "tenant", "identity-concurrent")
	draft := newIdentityDraft(t, s, owner)
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, payload := range []string{"capture-one", "capture-two"} {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			_, err := s.PutIdentityEvidence(ctx, PutIdentityEvidenceParams{Owner: owner, SessionID: draft.ID,
				Revision: draft.Revision, Kind: "selfie", JPEG: []byte(payload)})
			outcomes <- err
		}(payload)
	}
	wg.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrIdentityConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", successes, conflicts)
	}
}

func TestIdentityEvidenceRejectsMissingAuthorityBeforeDB(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	id := uuid.New()
	for _, tc := range []struct {
		owner IdentityOwner
		want  error
	}{{IdentityOwner{UserID: 1}, ErrMissingTenantID}, {IdentityOwner{TenantID: "tenant"}, ErrInvalidUserID},
		{IdentityOwner{TenantID: " tenant ", UserID: 1}, ErrInvalidTenantID}} {
		_, err := s.CreateIdentitySession(ctx, CreateIdentitySessionParams{Owner: tc.owner,
			SessionID: id, DocumentType: "passport", Synthetic: true})
		if !errors.Is(err, tc.want) {
			t.Fatalf("owner validation = %v, want %v", err, tc.want)
		}
	}
	owner := IdentityOwner{TenantID: "tenant", UserID: 1}
	if _, err := s.CreateIdentitySession(ctx, CreateIdentitySessionParams{Owner: owner,
		SessionID: id, DocumentType: "invalid-document"}); !errors.Is(err, ErrInvalidIdentityEvidence) {
		t.Fatalf("invalid document allowed: %v", err)
	}
	for _, value := range []IdentitySubmission{{}, {Revision: 1, ConsentVersion: IdentityConsentVersion,
		FieldsReviewed: true, HolderName: " untrimmed "}} {
		if err := ValidateIdentitySubmission(value); !errors.Is(err, ErrInvalidIdentityEvidence) {
			t.Fatalf("invalid submission = %v", err)
		}
	}
}
