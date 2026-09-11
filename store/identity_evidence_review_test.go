package store

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
)

func TestIdentityEvidenceRuntimeAuthorityAndTerminalRaces(t *testing.T) {
	ctx := t.Context()
	migration := newIdentityAuthTestStore(t, ctx)
	owner := identityTestOwner(t, migration, "tenant", "identity-reviewed-owner")
	other := identityTestOwner(t, migration, "tenant", "identity-reviewed-other")
	runtime := New(openMigrationAuthorityRoleDB(t, "identity_auth", "identity_auth_runtime"))
	var actualRole string
	if err := runtime.DB.GetContext(ctx, &actualRole, `SELECT current_user`); err != nil || actualRole != "identity_auth_runtime" {
		t.Fatalf("test must use actual runtime login: %s, %v", actualRole, err)
	}
	for _, competitor := range []string{"capture", "discard"} {
		t.Run(competitor, func(t *testing.T) {
			for range 4 {
				draft := newIdentityDraft(t, runtime, owner)
				draft = identityUpload(t, runtime, owner, draft, "document_front")
				draft = identityUpload(t, runtime, owner, draft, "selfie")
				submission := IdentitySubmission{Revision: draft.Revision, ConsentVersion: IdentityConsentVersion,
					FieldsReviewed: true, HolderName: "Synthetic Race Test"}
				if _, err := runtime.SubmitIdentitySession(ctx, other, draft.ID, submission); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("cross-owner submission: %v", err)
				}
				start := make(chan struct{})
				outcomes := make(chan error, 2)
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					_, err := runtime.SubmitIdentitySession(ctx, owner, draft.ID, submission)
					outcomes <- err
				}()
				go func() {
					defer wg.Done()
					<-start
					var err error
					if competitor == "capture" {
						_, err = runtime.PutIdentityEvidence(ctx, PutIdentityEvidenceParams{Owner: owner, SessionID: draft.ID,
							Revision: draft.Revision, Kind: "selfie", JPEG: []byte("synthetic-replacement-selfie")})
					} else {
						_, err = runtime.DiscardIdentitySession(ctx, owner, draft.ID, draft.Revision)
					}
					outcomes <- err
				}()
				close(start)
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
					t.Fatalf("terminal race winners=%d conflicts=%d", successes, conflicts)
				}
				result, err := runtime.GetIdentitySession(ctx, owner, draft.ID)
				if err != nil {
					t.Fatal(err)
				}
				if result.Revision != draft.Revision+1 {
					t.Fatalf("race advanced revision more than once: %+v", result)
				}
				if result.Status == "discarded" {
					if competitor != "discard" || len(result.Evidence) != 0 || len(result.Submission) != 0 {
						t.Fatal("discard retained submitted evidence")
					}
				} else if result.Status == "submitted" {
					if len(result.Evidence) != 2 || len(result.Submission) == 0 {
						t.Fatal("submitted snapshot lost evidence or consent")
					}
					for i := range result.Evidence {
						if result.Evidence[i] != draft.Evidence[i] {
							t.Fatal("evidence changed after winning submission")
						}
					}
				} else if result.Status != "draft" || competitor != "capture" || len(result.Submission) != 0 {
					t.Fatalf("invalid state after competing operations: %+v", result)
				}
			}
		})
	}
}
