package request

import "errors"

var (
	ErrInvalidRequest              = errors.New("invalid wallet transaction request")
	ErrForbiddenIdentityField      = errors.New("wallet transaction identity fields are gateway-owned")
	ErrForbiddenDepositField       = errors.New("deposit authority fields are server-owned")
	ErrMissingIdempotencyKey       = errors.New("missing wallet transaction idempotency key")
	ErrInvalidIdempotencyKey       = errors.New("invalid wallet transaction idempotency key")
	ErrMissingField                = errors.New("missing wallet transaction field")
	ErrInvalidAmount               = errors.New("invalid wallet transaction amount")
	ErrInvalidWalletID             = errors.New("invalid wallet transaction wallet id")
	ErrInvalidWalletPair           = errors.New("invalid wallet transaction wallet pair")
	ErrInvalidDestinationID        = errors.New("invalid wallet transaction destination id")
	ErrMissingReturnToSourcePolicy = errors.New("missing wallet transaction return-to-source policy")
	ErrMissingApprovalPolicy       = errors.New("missing wallet transaction approval policy")
	ErrMissingTimeout              = errors.New("missing wallet transaction timeout")
	ErrInvalidTimeout              = errors.New("invalid wallet transaction timeout")
)
