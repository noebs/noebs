package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

type ebsCall[Req any] func(ctx context.Context, tenantID string, req Req) (ebs_fields.EBSParserFields, error)

func ebsErrorDetails(res ebs_fields.EBSParserFields) ebs_fields.ErrorDetails {
	return ebs_fields.ErrorDetails{
		Code:    res.ResponseCode,
		Status:  ebs_fields.EBSError,
		Details: res,
		Message: ebs_fields.EBSError,
	}
}

func handleEBS[Req any](
	h *Handler,
	c *fiber.Ctx,
	req *Req,
	call ebsCall[Req],
) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	if err := bindJSON(c, req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	res, callErr := call(c.UserContext(), tenantID, *req)
	if callErr != nil {
		var ebsCallErr *ebs_fields.CallError
		if errors.As(callErr, &ebsCallErr) && ebsCallErr != nil {
			return jsonResponse(c, statusForError(callErr), ebsErrorDetails(ebsCallErr.Response))
		}
		return jsonResponse(c, statusForError(callErr), fiber.Map{"code": "bad_request", "message": callErr.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"ebs_response": res})
}
