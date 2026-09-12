package operationsui

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/adonese/noebs/store"
)

type IdentityView struct {
	TenantID    string
	CSRFToken   string
	Queue       []store.IdentityReviewQueueItem
	Case        *store.IdentityReviewCase
	Submission  store.IdentitySubmission
	OperationID string
	CanDecide   bool
	Limit       int
	Offset      int
	HasNext     bool
}

func IdentityPath(tenant string) string {
	return "/backoffice/t/" + url.PathEscape(tenant) + "/verifications"
}
func IdentityCasePath(tenant string, userID int64, session string) string {
	return IdentityPath(tenant) + "/" + strconv.FormatInt(userID, 10) + "/" + url.PathEscape(session)
}

func identityTime(value time.Time) string { return value.UTC().Format(time.RFC3339) }

func identityEvidencePath(tenant string, review store.IdentityReviewCase, kind string) string {
	return IdentityCasePath(tenant, review.Owner.UserID, review.Session.ID.String()) + "/evidence/" + url.PathEscape(kind) + "?revision=" + strconv.FormatInt(review.Session.Revision, 10)
}

func identityQueuePath(tenant string, limit, offset int) string {
	return IdentityPath(tenant) + "?limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
}

func RenderIdentity(ctx context.Context, view IdentityView) ([]byte, error) {
	var body bytes.Buffer
	err := IdentityPage(view).Render(ctx, &body)
	return body.Bytes(), err
}
