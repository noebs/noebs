package consumer

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

type CardAuthorization struct {
	CardID    string `json:"card_id"`
	RailUUID  string `json:"rail_uuid"`
	IPINBlock string `json:"ipin_block"`
}

type OpaqueBalanceRequest struct {
	UUID              string            `json:"uuid"`
	RequestClaim      string            `json:"request_claim"`
	CardAuthorization CardAuthorization `json:"card_authorization"`
}

type OpaqueBalanceResult struct {
	UUID    string         `json:"uuid"`
	Balance BalanceAmounts `json:"balance"`
}

type BalanceAmounts struct {
	Available float64 `json:"available"`
	Ledger    float64 `json:"ledger"`
}

type ClaimFundedCardOperationCommand struct {
	UserID           int64  `json:"user_id"`
	CardID           string `json:"card_id"`
	RailUUID         string `json:"rail_uuid"`
	Purpose          string `json:"purpose"`
	BodyClaim        string `json:"body_claim"`
	RailTranDateTime string `json:"rail_tran_date_time"`
}

type ClaimFundedCardOperationResult struct {
	Granted          bool   `json:"granted"`
	RailTranDateTime string `json:"rail_tran_date_time"`
	PAN              string `json:"pan,omitempty"`
	Expiry           string `json:"exp_date,omitempty"`
}

// BalanceInquiryRequestClaim matches the SDK's JCS claim for the safe balance
// semantics: version, operation purpose, and the owned opaque card ID.
func BalanceInquiryRequestClaim(cardID string) (string, error) {
	return store.FundedOperationBodyClaim(cardID, store.FundedPurposeBalanceInquiry)
}

func (s *Service) OpaqueBalance(ctx context.Context, tenantID string, userID int64, req OpaqueBalanceRequest, now time.Time) (OpaqueBalanceResult, error) {
	if s == nil {
		return OpaqueBalanceResult{}, ErrMissingService
	}
	if !s.NoebsConfig.OpaqueBalanceEnabled {
		return OpaqueBalanceResult{}, ErrFundedOperationsUnavailable
	}
	if s.Store == nil {
		return OpaqueBalanceResult{}, ErrMissingStore
	}
	if s.HTTPClient == nil {
		return OpaqueBalanceResult{}, ErrMissingHTTPClient
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	if userID <= 0 {
		return OpaqueBalanceResult{}, store.ErrInvalidUserID
	}
	if now.IsZero() {
		return OpaqueBalanceResult{}, store.ErrInvalidRailTranDateTime
	}
	operationUUID, err := store.NormalizeRailUUID(req.UUID)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	cardID, err := store.NormalizeCardID(req.CardAuthorization.CardID)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	railUUID, err := store.NormalizeRailUUID(req.CardAuthorization.RailUUID)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	if railUUID != operationUUID {
		return OpaqueBalanceResult{}, ErrOperationRailUUIDMismatch
	}
	expectedClaim, err := BalanceInquiryRequestClaim(cardID)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	if !canonicalRequestClaim(req.RequestClaim) || subtle.ConstantTimeCompare([]byte(req.RequestClaim), []byte(expectedClaim)) != 1 {
		return OpaqueBalanceResult{}, store.ErrFundedClaimMismatch
	}
	publicKey, _, _, err := parseEnrollmentRailKey(s.NoebsConfig.EBSConsumerKey)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	if err := validateEnrollmentIPINBlock(req.CardAuthorization.IPINBlock, publicKey.Size()); err != nil {
		return OpaqueBalanceResult{}, err
	}
	ebsEndpoint, err := opaqueBalanceEBSEndpoint(s.NoebsConfig.ConsumerIP)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}

	grant, err := s.claimFundedCardOperationInCardVault(ctx, tenantID, ClaimFundedCardOperationCommand{
		UserID:           userID,
		CardID:           cardID,
		RailUUID:         operationUUID,
		Purpose:          store.FundedPurposeBalanceInquiry,
		BodyClaim:        expectedClaim,
		RailTranDateTime: formatEBSRailTime(now),
	})
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	if !grant.Granted {
		if grant.PAN != "" || grant.Expiry != "" {
			return OpaqueBalanceResult{}, ErrUnsafeBalanceResponse
		}
		return s.reconcileOpaqueBalance(ctx, tenantID, operationUUID, now)
	}
	if grant.PAN == "" || grant.Expiry == "" || grant.RailTranDateTime == "" {
		return OpaqueBalanceResult{}, ErrUnsafeBalanceResponse
	}

	railRequest := ebs_fields.ConsumerBalanceFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: s.NoebsConfig.ConsumerID,
			TranDateTime:  grant.RailTranDateTime,
			UUID:          operationUUID,
		},
		ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
			Pan:     grant.PAN,
			Ipin:    req.CardAuthorization.IPINBlock,
			ExpDate: grant.Expiry,
		},
	}
	payload, err := json.Marshal(railRequest)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	code, response, callErr := ebs_fields.EBSHttpClientWithClient(s.HTTPClient, ebsEndpoint, payload)
	if callErr != nil {
		if response.UUID != operationUUID {
			return OpaqueBalanceResult{}, ErrFundedOutcomeUnknown
		}
		sanitizeFundedBalanceRailError(&response)
		recordErr := s.recordTransaction(ctx, tenantID, response.EBSResponse)
		return OpaqueBalanceResult{}, errors.Join(&ebs_fields.CallError{
			Status: code, Response: response, Err: ErrFundedRailRejected,
		}, recordErr)
	}
	if code != http.StatusOK || response.UUID != operationUUID {
		return OpaqueBalanceResult{}, ErrFundedOutcomeUnknown
	}
	return s.completeOpaqueBalance(ctx, tenantID, operationUUID, response)
}

func (s *Service) ClaimFundedCardOperationForUserID(ctx context.Context, tenantID string, cmd ClaimFundedCardOperationCommand, now time.Time) (ClaimFundedCardOperationResult, error) {
	if s == nil || s.Store == nil {
		return ClaimFundedCardOperationResult{}, ErrMissingStore
	}
	grant, err := s.Store.ClaimFundedCardOperation(ctx, tenantID, store.FundedOperationClaim{
		UserID:           cmd.UserID,
		CardID:           cmd.CardID,
		RailUUID:         cmd.RailUUID,
		Purpose:          cmd.Purpose,
		BodyClaim:        cmd.BodyClaim,
		RailTranDateTime: cmd.RailTranDateTime,
	}, now)
	if err != nil {
		return ClaimFundedCardOperationResult{}, err
	}
	return ClaimFundedCardOperationResult{
		Granted:          grant.Granted,
		RailTranDateTime: grant.RailTranDateTime,
		PAN:              grant.PAN,
		Expiry:           grant.Expiry,
	}, nil
}

func (s *Service) claimFundedCardOperationInCardVault(ctx context.Context, tenantID string, cmd ClaimFundedCardOperationCommand) (ClaimFundedCardOperationResult, error) {
	var result ClaimFundedCardOperationResult
	if err := s.doAdminServiceCommand(ctx, tenantID, cardVaultCommandTarget, "/internal/card-vault/funded-operations/claim", cmd, &result); err != nil {
		return ClaimFundedCardOperationResult{}, err
	}
	return result, nil
}

func (s *Service) reconcileOpaqueBalance(ctx context.Context, tenantID, operationUUID string, now time.Time) (OpaqueBalanceResult, error) {
	statusUUID, err := uuid.NewRandom()
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	request := ebs_fields.ConsumerTransactionStatusFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: s.NoebsConfig.ConsumerID,
			TranDateTime:  formatEBSRailTime(now),
			UUID:          statusUUID.String(),
		},
		OriginalTranUUID: operationUUID,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	endpoint := strings.TrimRight(s.NoebsConfig.ConsumerIP, "/") + "/" + ebs_fields.ConsumerTransactionStatusEndpoint
	code, response, callErr := ebs_fields.EBSHttpClientWithClient(s.HTTPClient, endpoint, payload)
	if callErr != nil || code != http.StatusOK || response.OriginalTransaction.UUID != operationUUID {
		return OpaqueBalanceResult{}, ErrFundedOutcomeUnknown
	}
	reconciled := ebs_fields.EBSParserFields{
		EBSMapFields: response.EBSMapFields,
		EBSResponse:  response.OriginalTransaction,
	}
	if reconciled.ResponseCode != ebs_fields.SUCCESS {
		sanitizeFundedBalanceRailError(&reconciled)
		recordErr := s.recordTransaction(ctx, tenantID, reconciled.EBSResponse)
		return OpaqueBalanceResult{}, errors.Join(&ebs_fields.CallError{
			Status: http.StatusBadGateway, Response: reconciled, Err: ErrFundedRailRejected,
		}, recordErr)
	}
	return s.completeOpaqueBalance(ctx, tenantID, operationUUID, reconciled)
}

func (s *Service) completeOpaqueBalance(ctx context.Context, tenantID, operationUUID string, response ebs_fields.EBSParserFields) (OpaqueBalanceResult, error) {
	if response.UUID != operationUUID || response.ResponseCode != ebs_fields.SUCCESS {
		return OpaqueBalanceResult{}, ErrFundedOutcomeUnknown
	}
	balance, err := balanceAmounts(response.Balance)
	if err != nil {
		return OpaqueBalanceResult{}, err
	}
	response.MaskPAN()
	response.ExpDate = ""
	response.WorkingKey = ""
	response.PubKeyValue = ""
	if err := s.recordTransaction(ctx, tenantID, response.EBSResponse); err != nil {
		return OpaqueBalanceResult{}, err
	}
	return OpaqueBalanceResult{UUID: operationUUID, Balance: balance}, nil
}

func canonicalRequestClaim(value string) bool {
	if len(value) != 67 || !strings.HasPrefix(value, "v1:") {
		return false
	}
	digest, err := hex.DecodeString(value[3:])
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == value[3:]
}

func formatEBSRailTime(now time.Time) string {
	return now.UTC().Format("020106150405")
}

func opaqueBalanceEBSEndpoint(baseURL string) (string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/" + ebs_fields.ConsumerBalanceEndpoint
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", ErrFundedOperationsUnavailable
	}
	return parsed.String(), nil
}

func balanceAmounts(balance map[string]any) (BalanceAmounts, error) {
	available, availableOK := balance["available"].(float64)
	ledger, ledgerOK := balance["leger"].(float64)
	if !availableOK || !ledgerOK || math.IsNaN(available) || math.IsInf(available, 0) || math.IsNaN(ledger) || math.IsInf(ledger, 0) {
		return BalanceAmounts{}, ErrUnsafeBalanceResponse
	}
	return BalanceAmounts{Available: available, Ledger: ledger}, nil
}

func sanitizeFundedBalanceRailError(response *ebs_fields.EBSParserFields) {
	response.MaskPAN()
	response.ExpDate = ""
	response.WorkingKey = ""
	response.PubKeyValue = ""
	response.Balance = nil
}
