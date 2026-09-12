package walletgrpc

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func adminTransactionResolution(req *walletv1.RenderWalletAdminRequest, tenant string, operator walletstore.OperatorIdentity) (walletstore.PSPManualResolution, error) {
	version, err := strconv.ParseInt(adminForm(req.Form, "expected_version"), 10, 64)
	if err != nil {
		return walletstore.PSPManualResolution{}, walletstore.ErrMissingStatusVersion
	}
	command := walletstore.PSPManualResolution{TenantID: tenant, ClientReference: adminPath(req, "client_reference"), OperatorID: operator.ID, ExpectedVersion: version, IdempotencyKey: adminForm(req.Form, "idempotency_key"), Status: adminForm(req.Form, "status"), FulfillmentMethod: adminForm(req.Form, "fulfillment_method"), Reason: strings.TrimSpace(adminForm(req.Form, "reason")), EvidenceReference: strings.TrimSpace(adminForm(req.Form, "evidence_reference")), SettlementReference: strings.TrimSpace(adminForm(req.Form, "settlement_reference"))}
	if err := walletstore.ValidatePSPManualResolution(command); err != nil {
		return walletstore.PSPManualResolution{}, err
	}
	return command, nil
}

func (s *Server) resolveAdminTransaction(ctx context.Context, req *walletv1.RenderWalletAdminRequest, operator walletstore.OperatorIdentity) (*walletv1.RenderWalletAdminResponse, error) {
	tenant, err := adminTenantIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	command, err := adminTransactionResolution(req, tenant, operator)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err = s.Service.Store.ResolvePSPTransaction(ctx, command); err != nil {
		if errors.Is(err, walletstore.ErrStatusVersionConflict) || errors.Is(err, walletstore.ErrManualResolutionConflict) || errors.Is(err, walletstore.ErrManualResolutionNotAllowed) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, mapError(err)
	}
	// The atomic signal outbox delivers the recorded outcome to the existing Temporal workflow.
	return adminRedirect("/admin/wallet/transactions/"+url.PathEscape(command.ClientReference), tenant), nil
}
