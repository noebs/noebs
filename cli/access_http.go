package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type tenantAccessHTTP struct{ service tenantaccess.API }

func registerTenantAccessRoutes(router *fiber.App, service tenantaccess.API) error {
	if router == nil || service == nil {
		return backofficeauth.ErrInvalidConfiguration
	}
	h := &tenantAccessHTTP{service: service}
	group := router.Group("/admin/access", gateway.InternalPrincipalIdentityMiddleware())
	group.Get("", h.list)
	group.Post("/lookup", h.lookup)
	group.Get("/:subject", h.inspect)
	group.Get("/:subject/edit", h.edit)
	group.Post("/:subject/changes", h.change)
	return nil
}

func accessActor(c *fiber.Ctx, permission tenantauth.Permission) (tenantaccess.Actor, string, error) {
	principal, ok := gateway.InternalPrincipalIdentity(c)
	if !ok || !principal.HasRole(tenantauth.RoleTenantAdmin) || principal.Permission() != permission {
		return tenantaccess.Actor{}, "", fiber.ErrForbidden
	}
	csrf := c.Request().Header.PeekAll(backofficeauth.HeaderCSRFToken)
	if len(csrf) != 1 || backofficeauth.ValidateCSRFToken(string(csrf[0])) != nil {
		return tenantaccess.Actor{}, "", fiber.ErrUnauthorized
	}
	if len(c.Request().URI().QueryString()) != 0 {
		return tenantaccess.Actor{}, "", fiber.ErrBadRequest
	}
	return tenantaccess.Actor{Issuer: principal.Issuer, Subject: principal.Subject, TenantID: principal.TenantID, Roles: principal.Roles(), Permissions: []tenantauth.Permission{permission}, SourceIP: principal.SourceIP, RequestID: c.Get(workloadauth.HeaderRequestID)}, string(csrf[0]), nil
}

func accessTarget(c *fiber.Ctx) (string, error) {
	raw := c.Params("subject")
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || id.String() != raw {
		return "", fiber.ErrBadRequest
	}
	return raw, nil
}

func (h *tenantAccessHTTP) list(c *fiber.Ctx) error {
	actor, csrf, err := accessActor(c, tenantauth.PermissionIdentityAccessRead)
	if err != nil {
		return err
	}
	accounts, err := h.service.List(c.UserContext(), actor)
	if err != nil {
		return accessHTTPError(c, err)
	}
	if accessWantsJSON(c) {
		return accessJSON(c, http.StatusOK, accounts)
	}
	return renderAccessPage(c, accessPage{TenantID: actor.TenantID, CSRF: csrf, Accounts: accounts, List: true})
}

func (h *tenantAccessHTTP) lookup(c *fiber.Ctx) error {
	actor, _, err := accessActor(c, tenantauth.PermissionIdentityAccessRead)
	if err != nil {
		return err
	}
	if len(c.Body()) > 1024 || strings.Split(c.Get(fiber.HeaderContentType), ";")[0] != fiber.MIMEApplicationForm {
		return fiber.ErrBadRequest
	}
	values, err := url.ParseQuery(string(c.Body()))
	if err != nil {
		return fiber.ErrBadRequest
	}
	for name, items := range values {
		if (name != "subject" && name != "_csrf") || len(items) != 1 {
			return fiber.ErrBadRequest
		}
	}
	if values.Get("_csrf") != "" && values.Get("_csrf") != c.Get(backofficeauth.HeaderCSRFToken) {
		return fiber.ErrForbidden
	}
	raw := values.Get("subject")
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || id.String() != raw {
		return fiber.ErrBadRequest
	}
	return c.Redirect(backofficeTenantPath(actor.TenantID, "access")+"/"+raw, http.StatusSeeOther)
}

func (h *tenantAccessHTTP) inspect(c *fiber.Ctx) error {
	actor, csrf, err := accessActor(c, tenantauth.PermissionIdentityAccessRead)
	if err != nil {
		return err
	}
	target, err := accessTarget(c)
	if err != nil {
		return err
	}
	account, err := h.service.Inspect(c.UserContext(), actor, target)
	if err != nil {
		return accessHTTPError(c, err)
	}
	history, err := h.service.History(c.UserContext(), actor, target)
	if err != nil {
		return accessHTTPError(c, err)
	}
	if accessWantsJSON(c) {
		return accessJSON(c, http.StatusOK, struct {
			Account tenantaccess.Account `json:"account"`
			History []tenantaccess.Audit `json:"history"`
		}{account, history})
	}
	return renderAccessPage(c, accessPage{TenantID: actor.TenantID, CSRF: csrf, Account: &account, History: history})
}

func (h *tenantAccessHTTP) edit(c *fiber.Ctx) error {
	actor, csrf, err := accessActor(c, tenantauth.PermissionIdentityAccessWrite)
	if err != nil {
		return err
	}
	target, err := accessTarget(c)
	if err != nil {
		return err
	}
	account, err := h.service.Inspect(c.UserContext(), actor, target)
	if err != nil {
		return accessHTTPError(c, err)
	}
	page := accessPage{TenantID: actor.TenantID, CSRF: csrf, Account: &account, Edit: true, OperationID: uuid.NewString()}
	if account.PendingOperationID != "" {
		history, err := h.service.History(c.UserContext(), actor, target)
		if err != nil {
			return accessHTTPError(c, err)
		}
		for _, audit := range history {
			if audit.OperationID == account.PendingOperationID && audit.Status == "pending" {
				page.Retry = &tenantaccess.ChangeRequest{OperationID: audit.OperationID, TargetSubject: target, ExpectedRevision: audit.ExpectedRevision, Reason: audit.Reason, GrantRoles: audit.GrantRoles, RevokeRoles: audit.RevokeRoles}
			}
		}
		if page.Retry == nil {
			return accessHTTPError(c, tenantaccess.ErrUnavailable)
		}
		page.Edit = false
	}
	return renderAccessPage(c, page)
}

func (h *tenantAccessHTTP) change(c *fiber.Ctx) error {
	actor, csrf, err := accessActor(c, tenantauth.PermissionIdentityAccessWrite)
	if err != nil {
		return err
	}
	target, err := accessTarget(c)
	if err != nil {
		return err
	}
	request, err := decodeAccessChange(c)
	if err != nil {
		return fiber.ErrBadRequest
	}
	request.TargetSubject = target
	result, err := h.service.Change(c.UserContext(), actor, request)
	if err != nil {
		if !accessWantsJSON(c) && errors.Is(err, tenantaccess.ErrUnavailable) {
			c.Status(http.StatusServiceUnavailable)
			return renderAccessPage(c, accessPage{TenantID: actor.TenantID, CSRF: csrf, Retry: &request, Error: "The result is not confirmed. Retry this same change to resume it safely."})
		}
		return accessHTTPError(c, err)
	}
	if accessWantsJSON(c) {
		return accessJSON(c, http.StatusOK, result)
	}
	return c.Redirect(backofficeTenantPath(actor.TenantID, "access")+"/"+url.PathEscape(target), http.StatusSeeOther)
}

type accessChangeBody struct {
	OperationID      string            `json:"operation_id"`
	ExpectedRevision string            `json:"expected_revision"`
	Reason           string            `json:"reason"`
	GrantRoles       []tenantauth.Role `json:"grant_roles"`
	RevokeRoles      []tenantauth.Role `json:"revoke_roles"`
}

func decodeAccessChange(c *fiber.Ctx) (tenantaccess.ChangeRequest, error) {
	if len(c.Body()) == 0 || len(c.Body()) > 8192 {
		return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
	}
	var body accessChangeBody
	switch strings.Split(c.Get(fiber.HeaderContentType), ";")[0] {
	case fiber.MIMEApplicationJSON:
		// Check field cardinality before decoding: duplicate operation/revision fields
		// must not be interpreted differently by the journal and a browser client.
		decoder := json.NewDecoder(bytes.NewReader(c.Body()))
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
		}
		seen := map[string]bool{}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return tenantaccess.ChangeRequest{}, err
			}
			name, ok := token.(string)
			switch name {
			case "operation_id", "expected_revision", "reason", "grant_roles", "revoke_roles":
			default:
				return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
			}
			if !ok || seen[name] {
				return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
			}
			seen[name] = true
			var raw json.RawMessage
			if err = decoder.Decode(&raw); err != nil {
				return tenantaccess.ChangeRequest{}, err
			}
		}
		decoder = json.NewDecoder(bytes.NewReader(c.Body()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			return tenantaccess.ChangeRequest{}, err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
		}
	case fiber.MIMEApplicationForm:
		body.GrantRoles = []tenantauth.Role{}
		body.RevokeRoles = []tenantauth.Role{}
		values, err := url.ParseQuery(string(c.Body()))
		if err != nil {
			return tenantaccess.ChangeRequest{}, err
		}
		for name, entries := range values {
			switch name {
			case "operation_id", "expected_revision", "reason":
				if len(entries) != 1 {
					return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
				}
			case "_csrf":
				if len(entries) != 1 || entries[0] != c.Get(backofficeauth.HeaderCSRFToken) {
					return tenantaccess.ChangeRequest{}, fiber.ErrForbidden
				}
			case "grant_roles", "revoke_roles":
				if len(entries) > 3 {
					return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
				}
			default:
				return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
			}
		}
		body.OperationID = values.Get("operation_id")
		body.ExpectedRevision = values.Get("expected_revision")
		body.Reason = values.Get("reason")
		for _, role := range values["grant_roles"] {
			body.GrantRoles = append(body.GrantRoles, tenantauth.Role(role))
		}
		for _, role := range values["revoke_roles"] {
			body.RevokeRoles = append(body.RevokeRoles, tenantauth.Role(role))
		}
	default:
		return tenantaccess.ChangeRequest{}, fiber.ErrUnsupportedMediaType
	}
	for _, roles := range [][]tenantauth.Role{body.GrantRoles, body.RevokeRoles} {
		slices.Sort(roles)
		for index, role := range roles {
			if _, err := tenantauth.ParseTenantRole(string(role)); err != nil || index > 0 && role == roles[index-1] {
				return tenantaccess.ChangeRequest{}, fiber.ErrBadRequest
			}
		}
	}
	return tenantaccess.ChangeRequest{OperationID: body.OperationID, ExpectedRevision: body.ExpectedRevision, Reason: strings.TrimSpace(body.Reason), GrantRoles: body.GrantRoles, RevokeRoles: body.RevokeRoles}, nil
}

func accessWantsJSON(c *fiber.Ctx) bool {
	return c.Get(fiber.HeaderAccept) == fiber.MIMEApplicationJSON
}
func accessJSON(c *fiber.Ctx, status int, value any) error {
	setBackofficeFiberHeaders(c)
	return c.Status(status).JSON(value)
}

func accessHTTPError(c *fiber.Ctx, err error) error {
	status, code, message := http.StatusServiceUnavailable, "unavailable", "Tenant access could not be confirmed. Retry the same operation."
	switch {
	case errors.Is(err, tenantaccess.ErrInvalidRequest):
		status, code, message = 400, "invalid_request", "Complete the role changes, revision and reason."
	case errors.Is(err, tenantaccess.ErrForbidden):
		status, code, message = 403, "forbidden", "You do not have permission to manage this tenant."
	case errors.Is(err, tenantaccess.ErrNotFound):
		status, code, message = 404, "not_found", "The selected account was not found."
	case errors.Is(err, tenantaccess.ErrRevisionConflict):
		status, code, message = 409, "revision_conflict", "Access changed after this form was opened. Inspect the latest roles before submitting a new change."
	case errors.Is(err, tenantaccess.ErrOperationConflict):
		status, code, message = 409, "operation_conflict", "This operation ID was already used with different terms. Inspect the account history."
	case errors.Is(err, tenantaccess.ErrLastAdministrator):
		status, code, message = 409, "last_administrator", "Grant administrator access to another account before revoking the last administrator."
	case errors.Is(err, tenantaccess.ErrPendingOperation):
		status, code, message = 409, "pending_operation", "An earlier change requires recovery. Open Change access to resume the pending operation."
	}
	if accessWantsJSON(c) {
		return accessJSON(c, status, fiber.Map{"code": code, "message": message})
	}
	c.Status(status)
	principal, _ := gateway.InternalPrincipalIdentity(c)
	return renderAccessPage(c, accessPage{TenantID: principal.TenantID, Error: message})
}

type accessPage struct {
	TenantID, CSRF, OperationID, Error string
	Retry                              *tenantaccess.ChangeRequest
	Accounts                           []tenantaccess.Account
	Account                            *tenantaccess.Account
	History                            []tenantaccess.Audit
	List, Edit                         bool
}

func renderAccessPage(c *fiber.Ctx, page accessPage) error {
	var body bytes.Buffer
	if err := tenantAccessPage.Execute(&body, page); err != nil {
		return err
	}
	setBackofficeFiberHeaders(c)
	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Send(body.Bytes())
}

//go:embed access.html
var tenantAccessTemplate string

var tenantAccessPage = template.Must(template.New("access").Funcs(template.FuncMap{
	"base": func(tenant string) string { return backofficeTenantPath(tenant, "access") },
	"roles": func() []tenantauth.Role {
		return []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin}
	},
}).Parse(tenantAccessTemplate))
