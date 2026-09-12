package operationsui

import (
	"context"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/identitystate"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

func TestIdentityReviewRetainsRevisionAndEscapesSubmittedClaims(t *testing.T) {
	item := store.IdentityReviewCase{Owner: store.IdentityOwner{TenantID: "tenant-a", UserID: 42}, AccountVerification: store.IdentityVerification{Status: "verified", Substatus: "manual"}, Session: store.IdentitySession{ID: uuid.New(), Status: "submitted", Revision: 7, Verification: identitystate.State{Status: "pending", Substatus: "manual_review"}, Evidence: []store.IdentityEvidenceMetadata{{Kind: "selfie", Bytes: 123}}}}
	body, err := RenderIdentity(context.Background(), IdentityView{TenantID: "tenant-a", CSRFToken: "csrf-token", Case: &item, Submission: store.IdentitySubmission{HolderName: `<script>alert(1)</script>`}, CanDecide: true, OperationID: "stable-operation"})
	if err != nil {
		t.Fatal(err)
	}
	output := string(body)
	for _, want := range []string{`name="revision" value="7"`, `name="operation_id" value="stable-operation"`, `name="_csrf" value="csrf-token"`, `hx-post=`, `Account verification status</dt><dd>verified / manual`, `Session verification status</dt><dd>pending / manual_review`, `/evidence/selfie?revision=7`, `&lt;script&gt;`, `hx-history="false"`} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(output, `<script>alert(1)`) {
		t.Fatal("submitted HTML was not escaped")
	}
	for _, status := range []string{"draft", "approved", "needs_information", "rejected", "withdrawn", "discarded"} {
		item.Session.Status = status
		output, err := RenderIdentity(context.Background(), IdentityView{TenantID: "tenant-a", Case: &item, CanDecide: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(output), `name="decision"`) {
			t.Errorf("decision exposed for %s", status)
		}
	}
	item.Session.Status = "submitted"
	body, err = RenderIdentity(context.Background(), IdentityView{TenantID: "tenant-a", Case: &item, CanDecide: false})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `name="decision"`) {
		t.Fatal("read-only operator sees decision form")
	}
}
