package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"image/jpeg"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type identitySessionResponse struct {
	store.IdentitySession
	LivenessStatus string `json:"liveness_status"`
	DocumentStatus string `json:"document_status"`
	ReviewStatus   string `json:"review_status"`
}

func identityResult(c *fiber.Ctx, result store.IdentitySession, err error) error {
	if err != nil {
		status, code, message := http.StatusInternalServerError, "identity_unavailable", "Identity evidence is temporarily unavailable"
		switch {
		case errors.Is(err, sql.ErrNoRows):
			status, code, message = http.StatusNotFound, "identity_not_found", "Identity session not found"
		case errors.Is(err, store.ErrIdentityConflict):
			status, code, message = http.StatusConflict, "identity_conflict", err.Error()
		case errors.Is(err, store.ErrIdentityIncomplete):
			status, code, message = http.StatusUnprocessableEntity, "identity_incomplete", err.Error()
		case errors.Is(err, store.ErrInvalidIdentityEvidence):
			status, code, message = http.StatusBadRequest, "invalid_identity_evidence", err.Error()
		}
		return c.Status(status).JSON(fiber.Map{"code": code, "message": message})
	}
	review := "not_submitted"
	if result.Status == "submitted" {
		review = "pending_review"
	} else if result.Status == "approved" || result.Status == "needs_information" || result.Status == "rejected" || result.Status == "withdrawn" {
		review = result.Status
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(identitySessionResponse{IdentitySession: result,
		LivenessStatus: "not_evaluated", DocumentStatus: "not_evaluated", ReviewStatus: review})
}

func identityOwner(c *fiber.Ctx) (store.IdentityOwner, error) {
	userID, err := authenticatedUserID(c)
	if err != nil {
		return store.IdentityOwner{}, fiber.NewError(http.StatusUnauthorized, "missing authenticated user")
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return store.IdentityOwner{}, fiber.NewError(http.StatusBadRequest, "missing tenant")
	}
	return store.IdentityOwner{TenantID: tenantID, UserID: userID}, nil
}

func identityID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil || id.String() != value {
		return uuid.Nil, store.ErrInvalidIdentityEvidence
	}
	return id, nil
}

func identityJSON(c *fiber.Ctx, target any) error {
	if len(c.Body()) > 4096 {
		return store.ErrInvalidIdentityEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return store.ErrInvalidIdentityEvidence
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return store.ErrInvalidIdentityEvidence
	}
	return nil
}

func identityRevision(c *fiber.Ctx) (int64, error) {
	value := c.Get("X-Identity-Revision")
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != value {
		return 0, store.ErrInvalidIdentityEvidence
	}
	return revision, nil
}

func (h *Handler) CreateIdentitySession(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	var input struct {
		SessionID         string `json:"session_id"`
		DocumentType      string `json:"document_type"`
		Synthetic         bool   `json:"synthetic"`
		PreviousSessionID string `json:"previous_session_id,omitempty"`
	}
	if err := identityJSON(c, &input); err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	id, err := identityID(input.SessionID)
	if err != nil || !store.ValidIdentityDocumentType(input.DocumentType) {
		return identityResult(c, store.IdentitySession{}, store.ErrInvalidIdentityEvidence)
	}
	var previous *uuid.UUID
	if input.PreviousSessionID != "" {
		parsed, err := identityID(input.PreviousSessionID)
		if err != nil {
			return identityResult(c, store.IdentitySession{}, err)
		}
		previous = &parsed
	}
	result, err := h.Service.CreateIdentitySession(c.UserContext(), store.CreateIdentitySessionParams{
		Owner: owner, SessionID: id, DocumentType: input.DocumentType, Synthetic: input.Synthetic, PreviousSessionID: previous})
	return identityResult(c, result, err)
}

func (h *Handler) LatestIdentitySession(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	result, err := h.Service.LatestIdentitySession(c.UserContext(), owner)
	return identityResult(c, result, err)
}

func (h *Handler) WithdrawIdentitySession(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	id, err := identityID(c.Params("session_id"))
	if err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if err := identityJSON(c, &input); err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	if input.Revision < 1 {
		return identityResult(c, store.IdentitySession{}, store.ErrInvalidIdentityEvidence)
	}
	result, err := h.Service.WithdrawIdentitySession(c.UserContext(), owner, id, input.Revision)
	return identityResult(c, result, err)
}

func (h *Handler) GetIdentitySession(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	id, err := identityID(c.Params("session_id"))
	if err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	result, err := h.Service.GetIdentitySession(c.UserContext(), owner, id)
	return identityResult(c, result, err)
}

func (h *Handler) PutIdentityEvidence(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	id, err := identityID(c.Params("session_id"))
	if err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	revision, err := identityRevision(c)
	if err != nil || !store.ValidIdentityEvidenceKind(c.Params("kind")) {
		return identityResult(c, store.IdentitySession{}, store.ErrInvalidIdentityEvidence)
	}
	if len(c.Body()) > store.MaxIdentityImageBytes {
		return c.Status(http.StatusRequestEntityTooLarge).JSON(fiber.Map{"code": "image_too_large"})
	}
	if c.Get(fiber.HeaderContentType) != "image/jpeg" {
		return c.Status(http.StatusUnsupportedMediaType).JSON(fiber.Map{"code": "jpeg_required"})
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(c.Body()))
	if err != nil || config.Width < 64 || config.Height < 64 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 12000000 {
		return identityResult(c, store.IdentitySession{}, store.ErrInvalidIdentityEvidence)
	}
	// Decode after bounding pixels so truncated or malformed JPEGs do not count
	// as received evidence. This checks encoding, not identity or authenticity.
	if _, err := jpeg.Decode(bytes.NewReader(c.Body())); err != nil {
		return identityResult(c, store.IdentitySession{}, store.ErrInvalidIdentityEvidence)
	}
	result, err := h.Service.PutIdentityEvidence(c.UserContext(), store.PutIdentityEvidenceParams{
		Owner: owner, SessionID: id, Revision: revision, Kind: c.Params("kind"), JPEG: c.Body()})
	return identityResult(c, result, err)
}

func (h *Handler) SubmitIdentitySession(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	id, err := identityID(c.Params("session_id"))
	if err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	var submission store.IdentitySubmission
	if err := identityJSON(c, &submission); err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	submission.HolderName = strings.TrimSpace(submission.HolderName)
	submission.DocumentNumber = strings.TrimSpace(submission.DocumentNumber)
	if err := store.ValidateIdentitySubmission(submission); err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	result, err := h.Service.SubmitIdentitySession(c.UserContext(), owner, id, submission)
	return identityResult(c, result, err)
}

func (h *Handler) DiscardIdentitySession(c *fiber.Ctx) error {
	owner, err := identityOwner(c)
	if err != nil {
		return err
	}
	id, err := identityID(c.Params("session_id"))
	if err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	revision, err := identityRevision(c)
	if err != nil {
		return identityResult(c, store.IdentitySession{}, err)
	}
	result, err := h.Service.DiscardIdentitySession(c.UserContext(), owner, id, revision)
	return identityResult(c, result, err)
}
