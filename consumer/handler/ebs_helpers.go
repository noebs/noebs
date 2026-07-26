package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

type ebsCall[Req any] func(ctx context.Context, tenantID string, req Req) (ebs_fields.EBSParserFields, error)

func authenticatedEBS(next fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID, err := authenticatedUserID(c)
		if err != nil {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing authenticated user identity"})
		}
		ctx, err := consumer.WithTransactionActor(c.UserContext(), userID)
		if err != nil {
			return jsonResponse(c, http.StatusUnauthorized, fiber.Map{"code": "unauthorized", "message": "missing authenticated user identity"})
		}
		c.SetUserContext(ctx)
		return next(c)
	}
}

type publicEBSErrorDetails struct {
	UUID            string `json:"UUID,omitempty"`
	ResponseMessage string `json:"responseMessage,omitempty"`
	ResponseStatus  string `json:"responseStatus,omitempty"`
	ResponseCode    int    `json:"responseCode"`
	TranDateTime    string `json:"tranDateTime,omitempty"`
}

func ebsErrorDetails(res ebs_fields.EBSParserFields) ebs_fields.ErrorDetails {
	return ebs_fields.ErrorDetails{
		Code:   res.ResponseCode,
		Status: ebs_fields.EBSError,
		Details: publicEBSErrorDetails{
			UUID:            res.UUID,
			ResponseMessage: res.ResponseMessage,
			ResponseStatus:  res.ResponseStatus,
			ResponseCode:    res.ResponseCode,
			TranDateTime:    res.TranDateTime,
		},
		Message: ebs_fields.EBSError,
	}
}

func handleEBS[Req any](
	c *fiber.Ctx,
	req *Req,
	call ebsCall[Req],
	successPayload func(ebs_fields.EBSParserFields) interface{},
) error {
	tenantID, err := prepareEBS(c, req)
	if err != nil {
		return err
	}

	return completeEBS(c, tenantID, *req, call, successPayload)
}

func handleConfiguredEBS[Req any](
	c *fiber.Ctx,
	req *Req,
	applyConfig func(*Req),
	call ebsCall[Req],
	successPayload func(ebs_fields.EBSParserFields) interface{},
) error {
	tenantID, err := parseEBS(c, req)
	if err != nil {
		return err
	}
	applyConfig(req)
	if err := ebs_fields.ValidateStruct(req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}

	return completeEBS(c, tenantID, *req, call, successPayload)
}

func prepareEBS[Req any](c *fiber.Ctx, req *Req) (string, error) {
	tenantID, err := parseEBS(c, req)
	if err != nil {
		return "", err
	}
	if err := ebs_fields.ValidateStruct(req); err != nil {
		return "", jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	return tenantID, nil
}

func parseEBS[Req any](c *fiber.Ctx, req *Req) (string, error) {
	if err := parseJSON(c, req); err != nil {
		return "", jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return "", jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	return tenantID, nil
}

func completeEBS[Req any](
	c *fiber.Ctx,
	tenantID string,
	req Req,
	call ebsCall[Req],
	successPayload func(ebs_fields.EBSParserFields) interface{},
) error {
	res, callErr := call(c.UserContext(), tenantID, req)
	if callErr != nil {
		var ebsCallErr *ebs_fields.CallError
		if errors.As(callErr, &ebsCallErr) && ebsCallErr != nil {
			if ebsCallErr.Response.ResponseCode == 0 && ebsCallErr.Response.ResponseMessage == "" {
				return jsonResponse(c, statusForError(callErr), apperr.Wrap(callErr, apperr.ErrBadGateway, ""))
			}
			return jsonResponse(c, statusForError(callErr), ebsErrorDetails(ebsCallErr.Response))
		}
		status := statusForError(callErr)
		if status >= http.StatusInternalServerError {
			base := apperr.ErrInternal
			if status == http.StatusBadGateway {
				base = apperr.ErrBadGateway
			} else if status == http.StatusServiceUnavailable {
				base = apperr.ErrUnavailable
			}
			return jsonResponse(c, status, apperr.Wrap(callErr, base, ""))
		}
		return jsonResponse(c, status, fiber.Map{"code": "bad_request", "message": callErr.Error()})
	}
	if successPayload != nil {
		return jsonResponse(c, http.StatusOK, successPayload(res))
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"ebs_response": res})
}
