package handler

import (
	"net/http"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

// CardFromNumber maps a submitted MSISDN to a stored PAN.
func (h *Handler) CardFromNumber(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	q := strings.TrimSpace(c.Query("mobile_number"))
	if q == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "mobile number is empty", "code": "empty_mobile_number"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	pan, err := h.Service.CardFromNumber(c.UserContext(), tenantID, q)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": pan})
}

func (h *Handler) GetCards(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	cards, main, err := h.Service.GetCardsByUserID(c.UserContext(), tenantID, userID)
	if err != nil {
		return jsonResponse(c, http.StatusNotFound, fiber.Map{"cards": nil, "main_card": nil})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"cards": cards, "main_card": main})
}

func (h *Handler) AddDeviceToken(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	username := getMobile(c)
	type data struct {
		Token string `json:"token"`
	}
	var req data
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.AddDeviceToken(c.UserContext(), tenantID, username, req.Token); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	return jsonResponse(c, http.StatusOK, nil)
}

func (h *Handler) CreateBeneficiary(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var req ebs_fields.Beneficiary
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if err := h.Service.UpsertBeneficiary(c.UserContext(), tenantID, mobile, req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	return jsonResponse(c, http.StatusCreated, nil)
}

func (h *Handler) ListBeneficiaries(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	list, err := h.Service.ListBeneficiaries(c.UserContext(), tenantID, mobile)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	return jsonResponse(c, http.StatusOK, list)
}

func (h *Handler) DeleteBeneficiary(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}

	var req ebs_fields.Beneficiary
	if err := parseJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if err := h.Service.DeleteBeneficiary(c.UserContext(), tenantID, mobile, req.Data); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	return jsonResponse(c, http.StatusNoContent, nil)
}

func (h *Handler) AddCards(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	userID := getUserID(c)
	var list []ebs_fields.Card
	if err := parseJSON(c, &list); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.AddCardsForUserID(c.UserContext(), tenantID, userID, list); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"code": "ok", "message": "cards added"})
}

func (h *Handler) EditCard(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req ebs_fields.Card
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "unmarshalling_error"})
	}
	userID := getUserID(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.EditCardForUserID(c.UserContext(), tenantID, userID, req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusCreated, fiber.Map{"result": "ok"})
}

func (h *Handler) RemoveCard(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	userID := getUserID(c)
	var card ebs_fields.Card
	if err := bindJSON(c, &card); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "unmarshalling_error"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.RemoveCardForUserID(c.UserContext(), tenantID, userID, card.CardIdx); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (h *Handler) NecToName(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	nec := strings.TrimSpace(c.Query("nec"))
	if nec == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "missing nec", "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	name, err := h.Service.NecToName(c.UserContext(), tenantID, nec)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "No user found with this NEC", "code": "nec_not_found"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": name})
}

func (h *Handler) Notifications(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	records, err := h.Service.Notifications(c.UserContext(), tenantID, mobile)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, records)
}

func (h *Handler) GetUser(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	profile, err := h.Service.GetUserProfile(c.UserContext(), tenantID, mobile)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, profile)
}

func (h *Handler) UpdateUser(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var profile ebs_fields.UserProfile
	if err := bindJSON(c, &profile); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "binding_error"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.UpdateUserProfile(c.UserContext(), tenantID, mobile, profile); err != nil {
		if err.Error() == "username already exists" {
			return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "duplication_error", "message": "username already exists"})
		}
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (h *Handler) GetUserLanguage(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	lang, err := h.Service.GetUserLanguage(c.UserContext(), tenantID, mobile)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"language": lang})
}

func (h *Handler) SetUserLanguage(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := getMobile(c)
	language := strings.TrimSpace(c.Query("language"))
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if language == "" {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "You must set a language", "code": "client_error"})
	}
	if err := h.Service.SetUserLanguage(c.UserContext(), tenantID, mobile, language); err != nil {
		return jsonResponse(c, http.StatusInternalServerError, fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (h *Handler) KYC(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	var req ebs_fields.KYCPassport
	if err := bindJSON(c, &req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	if err := h.Service.UpdateKYC(c.UserContext(), tenantID, req); err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	return jsonResponse(c, http.StatusOK, fiber.Map{"message": "KYC created successfully", "code": "ok"})
}

func (h *Handler) TransactionByUUID(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	id := strings.TrimSpace(c.Query("uuid"))
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	response, err := h.Service.GetTransactionByUUID(c.UserContext(), tenantID, id)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "not_found", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, response)
}

func (h *Handler) CardsByMobile(c *fiber.Ctx) error {
	if h == nil || h.Service == nil {
		return jsonResponse(c, http.StatusServiceUnavailable, fiber.Map{"code": "service_unavailable"})
	}
	mobile := strings.TrimSpace(c.Query("mobile"))
	tenantID, err := resolveTenantID(c)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "missing_tenant_id", "message": err.Error()})
	}
	response, err := h.Service.GetUserCards(c.UserContext(), tenantID, mobile)
	if err != nil {
		return jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "not_found", "message": err.Error()})
	}
	return jsonResponse(c, http.StatusOK, response)
}
