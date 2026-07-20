package request

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/transactionauth"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type Defaults struct {
	HoldExpirySeconds      int32
	ApprovalTimeoutSeconds int32
	ApprovalThreshold      int64
}

type Canonical struct {
	Operation      transactionauth.Operation
	Message        proto.Message
	Body           []byte
	Digest         transactionauth.Digest
	IdempotencyKey string
}

func ParseDeposit(tenantID string, body []byte) (*walletv1.RequestDepositRequest, error) {
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant_id", ErrInvalidRequest)
	}
	fields, err := objectFields(body)
	if err != nil {
		return nil, err
	}
	if hasAny(fields,
		"tenant_id", "tenantId",
		"client_reference", "clientReference",
		"owner_type", "ownerType", "owner_id", "ownerId",
		"psp_transaction_id", "pspTransactionId",
		"fee_amount", "feeAmount", "net_amount", "netAmount",
	) {
		return nil, ErrForbiddenDepositField
	}
	request := &walletv1.RequestDepositRequest{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, request); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	request.TenantId = tenantID
	request.ProviderCode = strings.TrimSpace(request.ProviderCode)
	request.WalletId = strings.TrimSpace(request.WalletId)
	request.Currency = strings.TrimSpace(request.Currency)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.Region = strings.TrimSpace(request.Region)
	if request.ProviderCode == "" || request.Currency == "" {
		return nil, ErrMissingField
	}
	walletID, err := canonicalUUID(request.WalletId)
	if err != nil {
		return nil, err
	}
	request.WalletId = walletID
	if request.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if request.IdempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}
	if len(request.IdempotencyKey) > 256 {
		return nil, ErrInvalidIdempotencyKey
	}
	return request, nil
}

func ParsePublic(
	operation transactionauth.Operation,
	tenantID string,
	body []byte,
	defaults Defaults,
) (Canonical, error) {
	return parse(operation, tenantID, body, defaults, true)
}

func ParseCanonical(
	operation transactionauth.Operation,
	tenantID string,
	body []byte,
) (Canonical, error) {
	return parse(operation, tenantID, body, Defaults{}, false)
}

func parse(
	operation transactionauth.Operation,
	tenantID string,
	body []byte,
	defaults Defaults,
	publicBoundary bool,
) (Canonical, error) {
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return Canonical{}, fmt.Errorf("%w: tenant_id", ErrInvalidRequest)
	}
	if len(body) == 0 || !operation.Valid() {
		return Canonical{}, ErrInvalidRequest
	}
	fields, err := objectFields(body)
	if err != nil {
		return Canonical{}, err
	}
	if hasAny(fields, "tenant_id", "tenantId") {
		return Canonical{}, ErrForbiddenIdentityField
	}

	var message proto.Message
	var idempotencyKey string
	switch operation {
	case transactionauth.OperationWalletP2P:
		if hasAny(fields, "from_owner_type", "fromOwnerType", "from_owner_id", "fromOwnerId") {
			return Canonical{}, ErrForbiddenIdentityField
		}
		request := &walletv1.RequestP2PTransferRequest{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, request); err != nil {
			return Canonical{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
		idempotencyKey, err = normalizeP2P(request, tenantID)
		message = request
	case transactionauth.OperationWalletWithdrawal:
		if hasAny(fields, "owner_type", "ownerType", "owner_id", "ownerId") {
			return Canonical{}, ErrForbiddenIdentityField
		}
		if publicBoundary && hasAny(fields, "approval_required", "approvalRequired") {
			return Canonical{}, ErrForbiddenIdentityField
		}
		request := &walletv1.RequestWithdrawalRequest{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, request); err != nil {
			return Canonical{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
		}
		idempotencyKey, err = normalizeWithdrawal(request, tenantID, defaults, publicBoundary)
		message = request
	}
	if err != nil {
		return Canonical{}, err
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return Canonical{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	digest := sha256.Sum256(encoded)
	publicMessage := proto.Clone(message)
	switch request := publicMessage.(type) {
	case *walletv1.RequestP2PTransferRequest:
		request.TenantId = ""
	case *walletv1.RequestWithdrawalRequest:
		request.TenantId = ""
	}
	canonicalBody, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(publicMessage)
	if err != nil {
		return Canonical{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return Canonical{
		Operation:      operation,
		Message:        message,
		Body:           canonicalBody,
		Digest:         transactionauth.Digest(digest),
		IdempotencyKey: idempotencyKey,
	}, nil
}

func normalizeP2P(request *walletv1.RequestP2PTransferRequest, tenantID string) (string, error) {
	request.TenantId = tenantID
	request.Currency = strings.TrimSpace(request.Currency)
	request.FromWalletId = strings.TrimSpace(request.FromWalletId)
	request.ToWalletId = strings.TrimSpace(request.ToWalletId)
	request.Description = strings.TrimSpace(request.Description)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.ReferenceId = strings.TrimSpace(request.ReferenceId)
	request.ToOwnerType = strings.TrimSpace(request.ToOwnerType)
	request.ToOwnerId = strings.TrimSpace(request.ToOwnerId)
	request.FromOwnerType = ""
	request.FromOwnerId = ""
	if request.Currency == "" || request.ToOwnerType == "" || request.ToOwnerId == "" {
		return "", ErrMissingField
	}
	fromWalletID, err := canonicalUUID(request.FromWalletId)
	if err != nil {
		return "", err
	}
	toWalletID, err := canonicalUUID(request.ToWalletId)
	if err != nil {
		return "", err
	}
	if fromWalletID == toWalletID {
		return "", ErrInvalidWalletPair
	}
	request.FromWalletId = fromWalletID
	request.ToWalletId = toWalletID
	if request.Amount <= 0 {
		return "", ErrInvalidAmount
	}
	if request.IdempotencyKey == "" {
		return "", ErrMissingIdempotencyKey
	}
	if len(request.IdempotencyKey) > 256 {
		return "", ErrInvalidIdempotencyKey
	}
	if request.ReferenceId == "" {
		return "", ErrMissingField
	}
	return request.IdempotencyKey, nil
}

func normalizeWithdrawal(
	request *walletv1.RequestWithdrawalRequest,
	tenantID string,
	defaults Defaults,
	publicBoundary bool,
) (string, error) {
	request.TenantId = tenantID
	request.ClientReference = strings.TrimSpace(request.ClientReference)
	request.ProviderCode = strings.TrimSpace(request.ProviderCode)
	request.WalletId = strings.TrimSpace(request.WalletId)
	request.Currency = strings.TrimSpace(request.Currency)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.Region = strings.TrimSpace(request.Region)
	request.OwnerType = ""
	request.OwnerId = ""
	if request.ClientReference == "" || request.ProviderCode == "" || request.Currency == "" {
		return "", ErrMissingField
	}
	walletID, err := canonicalUUID(request.WalletId)
	if err != nil {
		return "", err
	}
	request.WalletId = walletID
	if request.Amount <= 0 {
		return "", ErrInvalidAmount
	}
	if request.DestinationId < 0 {
		return "", ErrInvalidDestinationID
	}
	if request.AllowReturnToSource == nil {
		return "", ErrMissingReturnToSourcePolicy
	}
	if !request.GetAllowReturnToSource() && request.DestinationId <= 0 {
		return "", ErrMissingField
	}
	if request.HoldExpirySeconds < 0 || request.ApprovalTimeoutSeconds < 0 {
		return "", ErrInvalidTimeout
	}
	if publicBoundary && request.HoldExpirySeconds == 0 {
		request.HoldExpirySeconds = defaults.HoldExpirySeconds
	}
	if request.HoldExpirySeconds <= 0 {
		return "", ErrMissingTimeout
	}
	if publicBoundary {
		approvalRequired := defaults.ApprovalThreshold > 0 && request.Amount >= defaults.ApprovalThreshold
		request.ApprovalRequired = &approvalRequired
	}
	if request.ApprovalRequired == nil {
		return "", ErrMissingApprovalPolicy
	}
	approvalRequired := request.GetApprovalRequired()
	if publicBoundary && approvalRequired && request.ApprovalTimeoutSeconds == 0 {
		request.ApprovalTimeoutSeconds = defaults.ApprovalTimeoutSeconds
	}
	if approvalRequired && request.ApprovalTimeoutSeconds <= 0 {
		return "", ErrMissingTimeout
	}
	if !approvalRequired && request.ApprovalTimeoutSeconds != 0 {
		return "", ErrInvalidTimeout
	}
	if request.IdempotencyKey == "" {
		return "", ErrMissingIdempotencyKey
	}
	if len(request.IdempotencyKey) > 256 {
		return "", ErrInvalidIdempotencyKey
	}
	return request.IdempotencyKey, nil
}

func canonicalUUID(raw string) (string, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		return "", ErrInvalidWalletID
	}
	return parsed.String(), nil
}

func objectFields(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, ErrInvalidRequest
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidRequest
	}
	return fields, nil
}

func hasAny(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, ok := fields[name]; ok {
			return true
		}
	}
	return false
}
