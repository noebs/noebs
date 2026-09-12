package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/operationsui"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterIdentityReviewRoutes(router fiber.Router, h *Handler) {
	router.Get("", h.IdentityReviewQueue)
	router.Get("/:user_id/:session_id", h.IdentityReviewCase)
	router.Get("/:user_id/:session_id/evidence/:kind", h.IdentityReviewEvidence)
	router.Post("/:user_id/:session_id/decision", h.IdentityReviewDecision)
}

func identityReviewer(c *fiber.Ctx, permission tenantauth.Permission) (store.IdentityReviewer, string, bool, error) {
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok || principal.Permission() != permission || (!principal.HasRole(tenantauth.RoleBackoffice) && !principal.HasRole(tenantauth.RoleTenantAdmin)) || (permission == tenantauth.PermissionIdentityReviewDecide && !principal.HasRole(tenantauth.RoleTenantAdmin)) {
		return store.IdentityReviewer{}, "", false, fiber.ErrForbidden
	}
	tokens := c.Request().Header.PeekAll(backofficeauth.HeaderCSRFToken)
	if len(tokens) != 1 || backofficeauth.ValidateCSRFToken(string(tokens[0])) != nil {
		return store.IdentityReviewer{}, "", false, fiber.ErrUnauthorized
	}
	if c.Request().URI().QueryArgs().Has("tenant_id") || c.Request().PostArgs().Has("tenant_id") {
		return store.IdentityReviewer{}, "", false, fiber.ErrBadRequest
	}
	actor := principal.Issuer + "#" + principal.Subject
	if len(actor) > 256 {
		return store.IdentityReviewer{}, "", false, fiber.ErrBadRequest
	}
	return store.IdentityReviewer{TenantID: principal.TenantID, Actor: actor}, string(tokens[0]), principal.HasRole(tenantauth.RoleTenantAdmin), nil
}

func identityReviewTarget(c *fiber.Ctx, tenant string) (store.IdentityOwner, uuid.UUID, error) {
	userID, err := strconv.ParseInt(c.Params("user_id"), 10, 64)
	if err != nil || userID < 1 || strconv.FormatInt(userID, 10) != c.Params("user_id") {
		return store.IdentityOwner{}, uuid.Nil, store.ErrInvalidIdentityEvidence
	}
	id, err := identityID(c.Params("session_id"))
	return store.IdentityOwner{TenantID: tenant, UserID: userID}, id, err
}

func (h *Handler) IdentityReviewQueue(c *fiber.Ctx) error {
	reviewer, csrf, _, err := identityReviewer(c, tenantauth.PermissionIdentityReviewRead)
	if err != nil {
		return err
	}
	limit, offset := 50, 0
	if value := c.Query("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil {
			return fiber.ErrBadRequest
		}
	}
	if value := c.Query("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil {
			return fiber.ErrBadRequest
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
		return fiber.ErrBadRequest
	}
	items, err := h.Service.ListIdentityReviewQueue(c.UserContext(), reviewer, limit, offset)
	if err != nil {
		return identityReviewError(c, err)
	}
	return renderIdentityReview(c, operationsui.IdentityView{TenantID: reviewer.TenantID, CSRFToken: csrf, Queue: items, Limit: limit, Offset: offset, HasNext: len(items) == limit && offset+limit <= 100000})
}

func (h *Handler) IdentityReviewCase(c *fiber.Ctx) error {
	reviewer, csrf, canDecide, err := identityReviewer(c, tenantauth.PermissionIdentityReviewRead)
	if err != nil {
		return err
	}
	owner, id, err := identityReviewTarget(c, reviewer.TenantID)
	if err != nil {
		return identityReviewError(c, err)
	}
	item, err := h.Service.ReadIdentityReviewCase(c.UserContext(), reviewer, owner, id)
	if err != nil {
		return identityReviewError(c, err)
	}
	var submission store.IdentitySubmission
	if len(item.Session.Submission) > 0 {
		if err = json.Unmarshal(item.Session.Submission, &submission); err != nil {
			return identityReviewError(c, err)
		}
	}
	return renderIdentityReview(c, operationsui.IdentityView{TenantID: reviewer.TenantID, CSRFToken: csrf, Case: &item, Submission: submission, CanDecide: canDecide, OperationID: uuid.NewString()})
}

func (h *Handler) IdentityReviewEvidence(c *fiber.Ctx) error {
	reviewer, _, _, err := identityReviewer(c, tenantauth.PermissionIdentityReviewRead)
	if err != nil {
		return err
	}
	owner, id, err := identityReviewTarget(c, reviewer.TenantID)
	if err != nil {
		return identityReviewError(c, err)
	}
	revision, err := strconv.ParseInt(c.Query("revision"), 10, 64)
	if err != nil || revision < 1 || !store.ValidIdentityEvidenceKind(c.Params("kind")) {
		return identityReviewError(c, store.ErrInvalidIdentityEvidence)
	}
	image, err := h.Service.ReadIdentityReviewEvidence(c.UserContext(), reviewer, owner, id, revision, c.Params("kind"))
	if err != nil {
		return identityReviewError(c, err)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set(fiber.HeaderContentType, "image/jpeg")
	return c.Send(image)
}

func (h *Handler) IdentityReviewDecision(c *fiber.Ctx) error {
	reviewer, _, _, err := identityReviewer(c, tenantauth.PermissionIdentityReviewDecide)
	if err != nil {
		return err
	}
	owner, id, err := identityReviewTarget(c, reviewer.TenantID)
	if err != nil {
		return identityReviewError(c, err)
	}
	if len(c.Body()) > 8192 || !strings.HasPrefix(c.Get(fiber.HeaderContentType), fiber.MIMEApplicationForm) {
		return fiber.ErrBadRequest
	}
	for _, field := range []string{"operation_id", "revision", "decision", "reason", "policy_reference", "evidence_reviewed"} {
		if len(c.Request().PostArgs().PeekMulti(field)) != 1 {
			return identityReviewError(c, store.ErrInvalidIdentityEvidence)
		}
	}
	operation, err := identityID(c.FormValue("operation_id"))
	if err != nil {
		return identityReviewError(c, err)
	}
	revision, err := strconv.ParseInt(c.FormValue("revision"), 10, 64)
	if err != nil {
		return identityReviewError(c, store.ErrInvalidIdentityEvidence)
	}
	params := store.IdentityReviewDecisionParams{Reviewer: reviewer, Owner: owner, SessionID: id, OperationID: operation, Revision: revision, Decision: c.FormValue("decision"), Reason: strings.TrimSpace(c.FormValue("reason")), PolicyReference: strings.TrimSpace(c.FormValue("policy_reference")), EvidenceReviewed: c.FormValue("evidence_reviewed") == "true"}
	if err = store.ValidateIdentityReviewDecision(params); err != nil {
		return identityReviewError(c, err)
	}
	if _, err = h.Service.DecideIdentityReview(c.UserContext(), params); err != nil {
		return identityReviewError(c, err)
	}
	location := operationsui.IdentityCasePath(reviewer.TenantID, owner.UserID, id.String())
	if c.Get("HX-Request") == "true" {
		c.Set("HX-Redirect", location)
		return c.SendStatus(http.StatusOK)
	}
	return c.Redirect(location, http.StatusSeeOther)
}

func renderIdentityReview(c *fiber.Ctx, view operationsui.IdentityView) error {
	body, err := operationsui.RenderIdentity(c.UserContext(), view)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Send(body)
}

func identityReviewError(c *fiber.Ctx, err error) error {
	code, message := http.StatusInternalServerError, "The review could not be completed. Retry the same form."
	switch {
	case errors.Is(err, sql.ErrNoRows):
		code, message = http.StatusNotFound, "This verification case was not found."
	case errors.Is(err, store.ErrIdentityConflict):
		code, message = http.StatusConflict, "The case changed after you opened it. Reload the case and review the latest revision."
	case errors.Is(err, store.ErrIdentityReviewIncomplete):
		code, message = http.StatusUnprocessableEntity, "Open the current case and review every evidence image before submitting a decision."
	case errors.Is(err, store.ErrInvalidIdentityEvidence), errors.Is(err, store.ErrIdentityIncomplete):
		code, message = http.StatusUnprocessableEntity, "Complete all review fields and confirm that you reviewed the documents."
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	if c.Get("HX-Request") == "true" {
		c.Set("HX-Retarget", "#review-feedback")
		c.Set("HX-Reswap", "innerHTML")
	}
	return c.Status(code).SendString("<p role=\"alert\">" + html.EscapeString(message) + "</p>")
}
