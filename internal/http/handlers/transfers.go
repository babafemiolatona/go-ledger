package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-ledger/internal/ledger"
	"github.com/go-ledger/internal/money"
	"github.com/google/uuid"
)

type transferReq struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
}

func Transfer(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req transferReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid JSON")
			return
		}
		fromID, err := uuid.Parse(req.FromAccountID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid from_account_id")
			return
		}
		toID, err := uuid.Parse(req.ToAccountID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid to_account_id")
			return
		}
		amount, err := money.ParseAmount(req.Amount)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}
		if req.Currency == "" {
			writeErr(w, http.StatusBadRequest, "validation_error", "currency is required")
			return
		}
		txID, err := svc.Transfer(r.Context(), fromID, toID, amount, req.Currency)
		if err != nil {
			switch {
			case errors.Is(err, ledger.ErrInsufficientFunds):
				writeErr(w, http.StatusConflict, "insufficient_funds", "insufficient funds")
			case errors.Is(err, ledger.ErrNotFound):
				writeErr(w, http.StatusNotFound, "account_not_found", err.Error())
			case errors.Is(err, ledger.ErrSameAccount), errors.Is(err, ledger.ErrValidation):
				writeErr(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			default:
				if containsInsufficientFunds(err) {
					writeErr(w, http.StatusConflict, "insufficient_funds", "insufficient funds")
					return
				}
				writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
			}
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"transaction_id": txID.String()})
	}
}

func containsInsufficientFunds(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "insufficient_funds") || contains(s, "would go negative")
}
func contains(s, sub string) bool { return len(s) >= len(sub) && search(s, sub) }
func search(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
