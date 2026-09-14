package ledger

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrValidation        = errors.New("validation error")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrSameAccount       = errors.New("source and destination accounts must be different")
)
