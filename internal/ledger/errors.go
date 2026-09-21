package ledger

import "errors"

var (
	ErrNotFound            = errors.New("not found")
	ErrValidation          = errors.New("validation error")
	ErrForbidden           = errors.New("forbidden")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrSameAccount         = errors.New("source and destination accounts must be different")
	ErrIdempotencyInFlight = errors.New("idempotency conflict: request in progress, retry later")
	ErrIdempotencyMismatch = errors.New("idempotency key already used for different request")
)
