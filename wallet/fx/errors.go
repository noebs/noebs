package fx

import "errors"

var (
	ErrMissingProvider     = errors.New("missing fx provider")
	ErrUnknownProvider     = errors.New("unknown fx provider")
	ErrDuplicateProvider   = errors.New("duplicate fx provider")
	ErrInvalidSource       = errors.New("invalid fx source")
	ErrInvalidSourceHost   = errors.New("invalid fx source host")
	ErrMissingPairs        = errors.New("missing fx source pairs")
	ErrInvalidPair         = errors.New("invalid fx source pair")
	ErrUnsortedPairs       = errors.New("fx source pairs are not canonically sorted")
	ErrTemporary           = errors.New("temporary fx provider failure")
	ErrInvalidResponse     = errors.New("invalid fx provider response")
	ErrResponseTooLarge    = errors.New("fx provider response too large")
	ErrUnexpectedMediaType = errors.New("unexpected fx provider media type")
	ErrObservationNotFound = errors.New("fx observation not found")
)
