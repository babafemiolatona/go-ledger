package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-ledger/internal/ledger"
	"github.com/google/uuid"
)

func GetBalance(svc *ledger.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		acctID, err := uuid.Parse(idStr)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "validation_error", "invalid account id")
			return
		}
		bal, err := svc.GetBalance(r.Context(), acctID)
		if err != nil {
			if errors.Is(err, ledger.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "account_not_found", "account not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		writeJSON(w, http.StatusOK, bal)
	}
}
